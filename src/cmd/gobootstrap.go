package cmd

import (
	"archive/tar"
	"bufio"
	"bytes"
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

	"github.com/Masterminds/semver/v3"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Test seams — overridden in tests to avoid real downloads / corrupted runners.
var (
	goCacheDirFunc        = goCacheDir
	goDownloadURLsFunc    = goDownloadURLs
	verifyGoToolchainFunc = verifyGoToolchain
)

// resolvedGoMinor caches the resolved Go minor version so goSupportsFeature avoids re-running "go version".
var resolvedGoMinor int

// poisonedGoVersions maps a corrupting Go version to a known-good replacement to substitute instead.
var poisonedGoVersions = map[string]string{
	"1.24.13": "1.25.11",
}

// unpoisonGoVersion returns the version to use: itself if clean, or its replacement if poisoned. The bool reports whether it substituted.
func unpoisonGoVersion(version string) (string, bool) {
	if replacement, ok := poisonedGoVersions[version]; ok {
		return replacement, true
	}
	return version, false
}

// EnsureGoVersion checks whether Go is available in PATH and whether its
// version satisfies the project's go.mod requirement. If Go is missing or too
// old, it downloads the required version to a cache directory and updates
// PATH/GOROOT so that all subsequent commands (build, test, vet) use it.
//
// Call this early in main, before any cobra/build logic runs.
func EnsureGoVersion() error {
	goPath, lookErr := exec.LookPath("go")
	if lookErr != nil {
		logger.Info("go-bootstrap: go not in PATH (%v)", lookErr)
		return bootstrapGo("go not found in PATH")
	}

	logger.Info("go-bootstrap: found go at %s", goPath)

	// Check whether the installed version satisfies go.mod.
	required, err := requiredGoVersion()
	if err != nil || required == "" {
		recordGoMinor("")
		return nil // can't determine required version, proceed with what we have
	}

	installed, err := installedGoVersion()
	if err != nil {
		logger.Warn("go-bootstrap: cannot determine installed version (%v), proceeding", err)
		recordGoMinor(required)
		return nil
	}

	installedVer, err := semver.NewVersion(installed)
	if err != nil {
		logger.Warn("go-bootstrap: cannot parse installed version %q (%v), proceeding", installed, err)
		recordGoMinor(required)
		return nil
	}
	requiredVer, err := semver.NewVersion(required)
	if err != nil {
		logger.Warn("go-bootstrap: cannot parse required version %q (%v), proceeding", required, err)
		recordGoMinor(installed)
		return nil
	}

	if !installedVer.LessThan(requiredVer) {
		// Refuse a known-poisoned toolchain even though its version satisfies the
		// requirement: it corrupts the build cache (see poisonedGoVersions).
		// Re-bootstrap a known-good toolchain instead.
		if replacement, poisoned := unpoisonGoVersion(installed); poisoned {
			logger.Warn("go-bootstrap: installed Go %s is poisoned (corrupts the build cache); replacing with a known-good toolchain (>= %s)", installed, replacement)
			return bootstrapGo(fmt.Sprintf("installed %s is poisoned", installed))
		}
		// Some hosted runners ship a half-extracted Go: it runs and reports a version, but GOROOT is
		// incomplete. Probe before trusting it, and re-download like a missing/too-old Go on failure.
		if err := verifyGoToolchainFunc(goPath); err != nil {
			logger.Warn("go-bootstrap: installed Go %s is present but broken (%v); re-downloading", installed, err)
			return bootstrapGo(fmt.Sprintf("installed %s is broken: %v", installed, err))
		}
		logger.Info("go-bootstrap: installed Go %s satisfies required %s", installed, required)
		recordGoMinor(installed)
		return nil
	}

	logger.Info("go-bootstrap: installed Go %s is older than required %s", installed, required)
	return bootstrapGo(fmt.Sprintf("installed %s < required %s", installed, required))
}

// verifyGoToolchain loads the "runtime" package via goPath to catch a half-extracted GOROOT that
// runs and reports a version but cannot compile. It sets GOTOOLCHAIN=local in a directory with no
// go.mod, so neither an auto-downloaded toolchain nor a module directive can skew the result.
func verifyGoToolchain(goPath string) error {
	cmd := exec.Command(goPath, "list", "runtime")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	cmd.Dir = os.TempDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// bootstrapGo downloads the Go version specified in go.mod and updates
// PATH/GOROOT to use it.
func bootstrapGo(reason string) error {
	required, err := requiredGoVersion()
	if err != nil || required == "" {
		return fmt.Errorf("%s and cannot determine version from go.mod: %v", reason, err)
	}

	// Never download a poisoned version, even if go.mod asks for it directly.
	if replacement, poisoned := unpoisonGoVersion(required); poisoned {
		logger.Warn("go-bootstrap: required Go %s is poisoned; using known-good %s instead", required, replacement)
		required = replacement
	}

	logger.Info("go-bootstrap: bootstrapping Go %s...", required)

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
	newGoPath, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("bootstrap completed but go still not found in PATH: %w", err)
	}

	// A broken download must fail here loudly, not limp into a guaranteed compile failure.
	if err := verifyGoToolchainFunc(newGoPath); err != nil {
		return fmt.Errorf("bootstrapped Go %s at %s failed integrity probe: %w", required, goRoot, err)
	}

	logger.Info("go-bootstrap: using Go %s from %s %s", required, goRoot, fmtDuration(time.Since(bootstrapStart)))
	recordGoMinor(required)
	return nil
}

// recordGoMinor parses a dotted "X.Y.Z" version string and stores its minor
// component in resolvedGoMinor so that goSupportsFeature can use it without
// shelling out to "go version". If ver is empty or unparseable, it falls back
// to running "go version".
func recordGoMinor(ver string) {
	parts := strings.SplitN(ver, ".", 3)
	if len(parts) >= 2 {
		if minor, err := strconv.Atoi(parts[1]); err == nil {
			resolvedGoMinor = minor
			return
		}
	}
	// Fallback when ver is unparseable: run "go version" with GOTOOLCHAIN=local for the real version.
	fallback := exec.Command("go", "version")
	fallback.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := fallback.Output()
	if err != nil {
		return
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 {
		return
	}
	v := strings.TrimPrefix(fields[2], "go")
	parts = strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		if minor, err := strconv.Atoi(parts[1]); err == nil {
			resolvedGoMinor = minor
		}
	}
}

// installedGoVersion runs "go version" and extracts the version number.
// It forces GOTOOLCHAIN=local so that Go reports the real installed version
// rather than auto-downloading a stripped toolchain module that inflates the
// reported version.
func installedGoVersion() (string, error) {
	cmd := exec.Command("go", "version")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// Output format: "go version goX.Y.Z <goos>/<goarch>"
	fields := strings.Fields(string(out))
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "go") {
		return "", fmt.Errorf("unexpected go version output: %s", out)
	}
	return strings.TrimPrefix(fields[2], "go"), nil
}

// requiredGoVersion reads the go.mod file and returns the Go version needed.
// It prefers the "toolchain goX.Y.Z" directive (if present) over the "go X.Y.Z"
// directive, since the toolchain directive specifies the exact version to use.
// A release archive is named for its patch version, so a go.mod naming only
// major and minor is normalized to carry an explicit patch component.
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
			// "toolchain goX.Y.Z" -> "X.Y.Z"
			toolchainVer = strings.TrimPrefix(tc, "go")
		} else if goVer == "" && strings.HasPrefix(line, "go ") {
			goVer = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}

	// Prefer the toolchain directive — it's more specific.
	if toolchainVer != "" {
		return normalizeGoVersion(toolchainVer), nil
	}
	return normalizeGoVersion(goVer), nil
}

// normalizeGoVersion appends a missing patch component: a release archive is named
// "goX.Y.Z", so a bare "X.Y" must gain a patch component for the download URL.
func normalizeGoVersion(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) == 2 {
		// Only major and minor — append the patch component
		return v + ".0"
	}
	return v
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
	// hostos, not runtime: a cosmo APE reports runtime.GOOS=="cosmo", but toolchain layout follows the host OS.
	if hostos.GOOS() == "windows" {
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
	// hostos, not runtime: go.dev has no "cosmo" archives, so download for the host OS; GOARCH is already correct.
	archiveName := fmt.Sprintf("go%s.%s-%s.tar.gz", version, hostos.GOOS(), runtime.GOARCH)
	urls := goDownloadURLsFunc(archiveName)

	var resp *http.Response
	var lastErr error
	for _, url := range urls {
		// Mid-line progress fragment, completed below; bypasses the logger via rawStderr (see logging.go).
		fmt.Fprintf(rawStderr, "go-bootstrap: downloading %s", url)
		dlStart := time.Now()
		resp, lastErr = http.Get(url)
		if lastErr == nil && resp.StatusCode == http.StatusOK {
			fmt.Fprintf(rawStderr, " %s\n", fmtDuration(time.Since(dlStart)))
			break
		}
		if resp != nil {
			resp.Body.Close()
		}
		if lastErr != nil {
			fmt.Fprintf(rawStderr, "\n  FAIL %v\n", lastErr)
		} else {
			fmt.Fprintf(rawStderr, "\n  FAIL HTTP %d\n", resp.StatusCode)
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
		}
	}
	if lastErr != nil && (resp == nil || resp.StatusCode != http.StatusOK) {
		return fmt.Errorf("all download URLs failed: %w", lastErr)
	}
	defer resp.Body.Close()

	// Go archives have a top-level "go/" dir; extract into cacheDir, then rename "go" to "go<version>".
	tmpRoot := filepath.Join(cacheDir, "go")
	os.RemoveAll(tmpRoot) // clean any stale partial extraction

	// Mid-line progress fragment, completed below; bypasses the logger via rawStderr (see logging.go).
	fmt.Fprintf(rawStderr, "go-bootstrap: extracting...")
	extractStart := time.Now()
	if err := extractTarGz(resp.Body, cacheDir); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	fmt.Fprintf(rawStderr, " %s\n", fmtDuration(time.Since(extractStart)))

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
