package cmd

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Test seams — overridden in tests to avoid real downloads.
var (
	goCacheDirFunc     = goCacheDir
	goDownloadURLsFunc = goDownloadURLs
)

// EnsureGoVersion checks whether Go is available in PATH and whether its
// version satisfies the project's go.mod requirement. If Go is missing or too
// old, it downloads the required version to a cache directory and updates
// PATH/GOROOT so that all subsequent commands (build, test, vet) use it.
//
// Call this early in main, before any cobra/build logic runs.
func EnsureGoVersion() error {
	goPath, lookErr := exec.LookPath("go")
	if lookErr != nil {
		fmt.Fprintf(os.Stderr, "go-bootstrap: go not in PATH (%v)\n", lookErr)
		return bootstrapGo("go not found in PATH")
	}

	fmt.Fprintf(os.Stderr, "go-bootstrap: found go at %s\n", goPath)

	// Check whether the installed version satisfies go.mod.
	required, err := requiredGoVersion()
	if err != nil || required == "" {
		return nil // can't determine required version, proceed with what we have
	}

	installed, err := installedGoVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "go-bootstrap: cannot determine installed version (%v), proceeding\n", err)
		return nil
	}

	if !goVersionLessThan(installed, required) {
		fmt.Fprintf(os.Stderr, "go-bootstrap: installed Go %s satisfies required %s\n", installed, required)
		return nil
	}

	fmt.Fprintf(os.Stderr, "go-bootstrap: installed Go %s is older than required %s\n", installed, required)
	return bootstrapGo(fmt.Sprintf("installed %s < required %s", installed, required))
}

// bootstrapGo downloads the Go version specified in go.mod and updates
// PATH/GOROOT to use it.
func bootstrapGo(reason string) error {
	required, err := requiredGoVersion()
	if err != nil || required == "" {
		return fmt.Errorf("%s and cannot determine version from go.mod: %v", reason, err)
	}

	fmt.Fprintf(os.Stderr, "go-bootstrap: bootstrapping Go %s...\n", required)

	bootstrapStart := time.Now()
	goRoot, err := ensureGoCached(required)
	if err != nil {
		return fmt.Errorf("failed to bootstrap Go %s: %w", required, err)
	}

	// Point subsequent processes at the cached toolchain
	goBin := filepath.Join(goRoot, "bin")
	os.Setenv("PATH", goBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.Setenv("GOROOT", goRoot)

	// Verify the bootstrapped Go is actually usable
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("bootstrap completed but go still not found in PATH: %w", err)
	}

	fmt.Fprintf(os.Stderr, "go-bootstrap: using Go %s from %s %s\n", required, goRoot, fmtDuration(time.Since(bootstrapStart)))
	return nil
}

// installedGoVersion runs "go version" and extracts the version number.
func installedGoVersion() (string, error) {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return "", err
	}
	// Output format: "go version go1.24.11 linux/amd64"
	fields := strings.Fields(string(out))
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "go") {
		return "", fmt.Errorf("unexpected go version output: %s", out)
	}
	return strings.TrimPrefix(fields[2], "go"), nil
}

// goVersionLessThan returns true if version a < version b.
// Versions are in the form "1.24.11" or "1.25".
func goVersionLessThan(a, b string) bool {
	ap := parseGoVersion(a)
	bp := parseGoVersion(b)
	for i := 0; i < 3; i++ {
		if ap[i] != bp[i] {
			return ap[i] < bp[i]
		}
	}
	return false
}

// parseGoVersion splits a Go version string into [major, minor, patch].
// Pre-release suffixes (rc1, beta2) are stripped.
func parseGoVersion(v string) [3]int {
	// Strip rc/beta suffix: "1.25rc1" -> "1.25", "1.25.0-beta1" -> "1.25.0"
	for _, sep := range []string{"-", "rc", "beta"} {
		if i := strings.Index(v, sep); i >= 0 {
			v = v[:i]
		}
	}
	parts := strings.Split(v, ".")
	var result [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		result[i], _ = strconv.Atoi(parts[i])
	}
	return result
}

// requiredGoVersion reads the go.mod file and returns the Go version needed.
// It prefers the "toolchain goX.Y.Z" directive (if present) over the "go X.Y.Z"
// directive, since the toolchain directive specifies the exact version to use.
func requiredGoVersion() (string, error) {
	f, err := os.Open("go.mod")
	if err != nil {
		return "", err
	}
	defer f.Close()

	var goVer, toolchainVer string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "toolchain ") {
			tc := strings.TrimSpace(strings.TrimPrefix(line, "toolchain "))
			// "toolchain go1.25.0" -> "1.25.0"
			toolchainVer = strings.TrimPrefix(tc, "go")
		} else if goVer == "" && strings.HasPrefix(line, "go ") {
			goVer = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}

	// Prefer the toolchain directive — it's more specific.
	if toolchainVer != "" {
		return toolchainVer, nil
	}
	return goVer, nil
}

// ensureGoCached downloads Go to ~/.cache/go-toolchain/go<version>/ if not
// already present. Returns the GOROOT path.
func ensureGoCached(version string) (string, error) {
	cacheDir, err := goCacheDirFunc()
	if err != nil {
		return "", err
	}

	goRoot := filepath.Join(cacheDir, "go"+version)
	goBin := filepath.Join(goRoot, "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}

	// Already downloaded?
	if _, err := os.Stat(goBin); err == nil {
		return goRoot, nil
	}

	// Download
	if err := downloadGo(version, cacheDir, goRoot); err != nil {
		// Clean up partial download
		os.RemoveAll(goRoot)
		return "", err
	}
	return goRoot, nil
}

func goCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		// Fallback for systems without XDG
		home, err2 := os.UserHomeDir()
		if err2 != nil {
			return "", fmt.Errorf("cannot determine cache directory: %w", err)
		}
		dir = filepath.Join(home, ".cache")
	}
	cacheDir := filepath.Join(dir, "go-toolchain")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}
	return cacheDir, nil
}

func downloadGo(version, cacheDir, goRoot string) error {
	archiveName := fmt.Sprintf("go%s.%s-%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	urls := goDownloadURLsFunc(archiveName)

	var resp *http.Response
	var lastErr error
	for _, url := range urls {
		fmt.Fprintf(os.Stderr, "go-bootstrap: downloading %s", url)
		dlStart := time.Now()
		resp, lastErr = http.Get(url)
		if lastErr == nil && resp.StatusCode == http.StatusOK {
			fmt.Fprintf(os.Stderr, " %s\n", fmtDuration(time.Since(dlStart)))
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if lastErr != nil {
			fmt.Fprintf(os.Stderr, "\n  FAIL %v\n", lastErr)
		} else {
			fmt.Fprintf(os.Stderr, "\n  FAIL HTTP %d\n", resp.StatusCode)
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
		}
	}
	if lastErr != nil && (resp == nil || resp.StatusCode != http.StatusOK) {
		return fmt.Errorf("all download URLs failed: %w", lastErr)
	}
	defer resp.Body.Close()

	// Extract tar.gz — Go archives contain a top-level "go/" directory.
	// We extract into cacheDir then rename "go" -> "go<version>".
	tmpRoot := filepath.Join(cacheDir, "go")
	os.RemoveAll(tmpRoot) // clean any stale partial extraction

	fmt.Fprintf(os.Stderr, "go-bootstrap: extracting...")
	extractStart := time.Now()
	if err := extractTarGz(resp.Body, cacheDir); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	fmt.Fprintf(os.Stderr, " %s\n", fmtDuration(time.Since(extractStart)))

	// Rename go/ -> go<version>/
	if err := os.Rename(tmpRoot, goRoot); err != nil {
		return fmt.Errorf("rename failed: %w", err)
	}

	return nil
}

// goDownloadURLs returns the list of URLs to try, in order.
// It uses the GOPROXY_FALLBACK env var as a CORS/firewall proxy prefix if set.
func goDownloadURLs(archiveName string) []string {
	primary := "https://go.dev/dl/" + archiveName
	mirror := "https://dl.google.com/go/" + archiveName

	proxy := os.Getenv("GOPROXY_FALLBACK")
	if proxy == "" {
		return []string{primary, mirror}
	}

	proxy = strings.TrimRight(proxy, "/")
	return []string{
		primary,
		mirror,
		proxy + "/https://go.dev/dl/" + archiveName,
		proxy + "/https://dl.google.com/go/" + archiveName,
	}
}

func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, hdr.Name)

		// Guard against path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
	return nil
}
