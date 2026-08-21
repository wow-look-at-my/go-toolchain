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

// resolvedGoMinor holds the minor version of the Go toolchain that
// EnsureGoVersion resolved (e.g. 24 for Go 1.24.7, 25 for Go 1.25.0).
// Set once during bootstrap; used by goSupportsFeature to avoid re-running
// "go version" (which can fail to find the bootstrapped binary or mis-parse
// non-standard version strings).
var resolvedGoMinor int

// poisonedGoVersions maps an exact Go version that is known to corrupt builds
// to the known-good version go-toolchain must use instead. Go 1.24.13
// intermittently cross-contaminates GOCACHEPROG cache entries -- it serves one
// package's compiled object under another package's action key -- so a build
// dies at package load with `"<pkg>" imported as <other>` (e.g. runtime
// imported as reflectlite) or `package runtime is not in std`. The corruption
// is specific to that patch release (1.24.7 and 1.25.0 are clean) and the
// `go list runtime` integrity probe does NOT surface it (that probe runs
// without the cacheprog), so the version must be blocklisted explicitly:
// go-toolchain treats it as unusable and silently substitutes the replacement,
// whether it is the runner's preinstalled Go or a go.mod requirement.
// The replacement is a current 1.25.x rather than 1.25.0 so the substitution is
// not a security downgrade: 1.24 is EOL (1.24.13 is the final 1.24.x, no
// 1.24.14 will ship), and 1.24.13 itself carried CVE fixes that 1.25.0 predates.
var poisonedGoVersions = map[string]string{
	"1.24.13": "1.25.11",
}

// unpoisonGoVersion returns the Go version go-toolchain should actually use for
// the given requested/installed version: the version itself when clean, or its
// known-good replacement when poisoned. The bool reports whether a substitution
// was made.
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
		// Version is fine, but a fraction of GitHub-hosted runners ship a
		// half-extracted Go whose binary runs and reports its version yet whose
		// GOROOT std library is incomplete. Probe the toolchain before trusting
		// it; if it's broken, treat it exactly like a missing/too-old Go and
		// re-download a known-good copy.
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

// verifyGoToolchain confirms a Go toolchain's standard library is intact by
// forcing it to load a core std package ("runtime"). It catches corrupted
// hosted-tool-cache installs — e.g. GitHub-hosted runners that ship a
// half-extracted Go whose GOROOT is missing or has an incomplete src/runtime,
// where the go binary still runs and reports its version but the first real
// compile dies with "package runtime is not in std".
//
// It runs the resolved goPath — the same binary the build will use — so it
// exercises the actual toolchain, with:
//   - GOTOOLCHAIN=local, so it probes the on-disk toolchain instead of letting
//     Go auto-download a module toolchain that would mask the breakage;
//   - a neutral working directory (no go.mod), so neither a local toolchain
//     directive nor a module's version requirement can influence the result.
//
// On a healthy toolchain "go list runtime" is sub-second (~20ms) and exits zero;
// on a broken one it exits non-zero, so this is cheap enough to run on every
// invocation.
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

	// Sanity assert: the freshly downloaded toolchain must pass the same integrity
	// probe we use on preinstalled Go. If a clean download is somehow still broken,
	// fail loudly here instead of limping into a guaranteed compile failure.
	if err := verifyGoToolchainFunc(newGoPath); err != nil {
		return fmt.Errorf("bootstrapped Go %s at %s failed integrity probe: %w", required, goRoot, err)
	}

	logger.Info("go-bootstrap: using Go %s from %s %s", required, goRoot, fmtDuration(time.Since(bootstrapStart)))
	recordGoMinor(required)
	return nil
}

// recordGoMinor parses a version string like "1.24.7" and stores its minor
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
	// Fallback: run "go version" for cases where we couldn't determine version.
	// Force GOTOOLCHAIN=local so we get the real system Go version, not an
	// auto-downloaded stripped toolchain module.
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
	// Output format: "go version go1.24.11 linux/amd64"
	fields := strings.Fields(string(out))
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "go") {
		return "", fmt.Errorf("unexpected go version output: %s", out)
	}
	return strings.TrimPrefix(fields[2], "go"), nil
}

// requiredGoVersion reads the go.mod file and returns the Go version needed.
// It prefers the "toolchain goX.Y.Z" directive (if present) over the "go X.Y.Z"
// directive, since the toolchain directive specifies the exact version to use.
// Since Go 1.21, release archives include the patch version (e.g. go1.25.0),
// so if go.mod says "go 1.25" we normalize it to "1.25.0".
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
		return normalizeGoVersion(toolchainVer), nil
	}
	return normalizeGoVersion(goVer), nil
}

// normalizeGoVersion ensures the version has a patch component.
// Starting with Go 1.21, release archives are named go1.X.0 rather than go1.X,
// so "1.25" must become "1.25.0" for the download URL to work.
func normalizeGoVersion(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) == 2 {
		// Only major.minor (e.g. "1.25") — append ".0"
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
	// hostos, not runtime: a GOOS=cosmo fat APE reports runtime.GOOS=="cosmo"
	// on every host, but the downloaded toolchain layout follows the HOST OS.
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
	// hostos, not runtime: go.dev has no "cosmo" archives — a cosmo fat APE
	// must download the toolchain for the host it is running on (e.g.
	// go1.x.linux-amd64.tar.gz). runtime.GOARCH is correct as-is: the running
	// payload of a fat APE always matches the host architecture.
	archiveName := fmt.Sprintf("go%s.%s-%s.tar.gz", version, hostos.GOOS(), runtime.GOARCH)
	urls := goDownloadURLsFunc(archiveName)

	var resp *http.Response
	var lastErr error
	for _, url := range urls {
		// Mid-line progress fragment (completed below on the same line):
		// bypasses the logger via rawStderr, see logging.go.
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

	// Extract tar.gz — Go archives contain a top-level "go/" directory.
	// We extract into cacheDir then rename "go" -> "go<version>".
	tmpRoot := filepath.Join(cacheDir, "go")
	os.RemoveAll(tmpRoot) // clean any stale partial extraction

	// Mid-line progress fragment (completed below on the same line):
	// bypasses the logger via rawStderr, see logging.go.
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
