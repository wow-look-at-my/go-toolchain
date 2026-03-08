package cmd

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"archive/tar"
)

// Test seams — overridden in tests to avoid real downloads.
var (
	goCacheDirFunc     = goCacheDir
	goDownloadURLsFunc = goDownloadURLs
)

// EnsureGoVersion checks whether the system Go satisfies the project's go.mod
// requirement. If Go is not installed or the installed version is too old, it
// downloads the required version to a cache directory and updates PATH +
// GOTOOLCHAIN so that all subsequent commands use the correct toolchain.
//
// Call this early in main, before any cobra/build logic runs.
func EnsureGoVersion() error {
	required, err := requiredGoVersion()
	if err != nil || required == "" {
		return nil // no go.mod or can't parse — let Go handle it
	}

	installed := installedGoVersion()

	if installed == "" {
		// Go is not installed at all — bootstrap it
		fmt.Printf("==> Go not found in PATH, bootstrapping Go %s...\n", required)
	} else if !goVersionLessThan(installed, required) {
		return nil // installed >= required, nothing to do
	} else {
		fmt.Printf("==> Go %s required (have %s), bootstrapping...\n", required, installed)
	}

	goRoot, err := ensureGoCached(required)
	if err != nil {
		return fmt.Errorf("failed to bootstrap Go %s: %w", required, err)
	}

	// Point subsequent processes at the cached toolchain
	goBin := filepath.Join(goRoot, "bin")
	os.Setenv("PATH", goBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.Setenv("GOROOT", goRoot)
	os.Setenv("GOTOOLCHAIN", "local")

	fmt.Printf("==> Using Go %s from %s\n", required, goRoot)
	return nil
}

// requiredGoVersion reads the "go X.Y.Z" directive from ./go.mod.
func requiredGoVersion() (string, error) {
	f, err := os.Open("go.mod")
	if err != nil {
		return "", err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "go ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "go ")), nil
		}
	}
	return "", nil
}

// installedGoVersion returns the version string (e.g. "1.24.7") from `go env GOVERSION`.
func installedGoVersion() string {
	out, err := exec.Command("go", "env", "GOVERSION").Output()
	if err != nil {
		return ""
	}
	v := strings.TrimSpace(string(out))
	return strings.TrimPrefix(v, "go")
}

// goVersionLessThan returns true if a < b using simple numeric comparison
// of major.minor.patch components.
func goVersionLessThan(a, b string) bool {
	pa := parseVersion(a)
	pb := parseVersion(b)
	for i := 0; i < 3; i++ {
		if pa[i] < pb[i] {
			return true
		}
		if pa[i] > pb[i] {
			return false
		}
	}
	return false
}

func parseVersion(v string) [3]int {
	var parts [3]int
	v = strings.TrimPrefix(v, "go")
	segs := strings.SplitN(v, ".", 3)
	for i, s := range segs {
		if i >= 3 {
			break
		}
		fmt.Sscanf(s, "%d", &parts[i])
	}
	return parts
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
		fmt.Printf("==> Downloading %s ...\n", url)
		resp, lastErr = http.Get(url)
		if lastErr == nil && resp.StatusCode == http.StatusOK {
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if lastErr != nil {
			fmt.Printf("  FAIL %v\n", lastErr)
		} else {
			fmt.Printf("  FAIL HTTP %d\n", resp.StatusCode)
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

	if err := extractTarGz(resp.Body, cacheDir); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}

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
