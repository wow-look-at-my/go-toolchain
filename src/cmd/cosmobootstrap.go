package cmd

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

const (
	defaultCosmoBranch   = "master"
	cosmoProbeTimeout    = 30 * time.Second
	cosmoDownloadTimeout = 10 * time.Minute
	// A timeout is a bad moment rather than an answer, so a strict caller asks again.
	cosmoProbeAttempts   = 3
	cosmoProbeRetryDelay = 2 * time.Second
)

// Test seams — overridden in tests to avoid real downloads and version probes.
var (
	ensureCosmoToolchainFunc = EnsureCosmoToolchain
	cosmoDownloadBase        = "https://dl.pazer.build/gosmopolitan"
	cosmoHostPlatformFunc    = cosmoHostPlatform
	cosmoGoVersionFunc       = cosmoGoVersion
	// The retry wait, so a test drives every attempt without sleeping.
	cosmoProbeSleep = time.Sleep
)

// cosmoHostPlatform returns the runnable platform: hostos.GOOS() (a fat APE reports "cosmo" via runtime.GOOS) plus runtime.GOARCH.
func cosmoHostPlatform() (goos, goarch string) {
	return hostos.GOOS(), runtime.GOARCH
}

// EnsureCosmoToolchain resolves a gosmopolitan Go toolchain (the Go fork that
// builds GOOS=cosmo fat APEs) and returns its GOROOT. It downloads from
// buildhost (dl.pazer.build/gosmopolitan, the master branch) and caches it
// under the same cache root the Go bootstrap uses
// (~/.cache/go-toolchain/cosmo/).
//
// The cache is keyed by the buildhost release version parsed from the dl
// endpoint's redirect (v<N>), so a cached toolchain is never re-downloaded
// and a new buildhost release is picked up automatically. If the redirect
// cannot be parsed, the cache falls back to a branch-keyed directory that is
// downloaded a single time and then reused as long as it exists.
func EnsureCosmoToolchain() (string, error) {
	hostOS, hostArch := cosmoHostPlatformFunc()

	dlURL := cosmoDownloadURL(defaultCosmoBranch, hostOS, hostArch)

	cacheDir, err := goCacheDirFunc()
	if err != nil {
		return "", err
	}
	cosmoCache := filepath.Join(cacheDir, "cosmo")

	key := cosmoCacheKey(dlURL, defaultCosmoBranch)
	goRoot := filepath.Join(cosmoCache, key, "go")
	if _, statErr := os.Stat(cosmoGoBinPath(goRoot)); statErr == nil {
		ver, verErr := cosmoGoVersionFunc(goRoot)
		if verErr != nil {
			return "", fmt.Errorf("cached gosmopolitan toolchain at %s is broken: %w (delete it to re-download)", goRoot, verErr)
		}
		logger.Info("cosmo-bootstrap: using cached %s from %s (%s)", key, goRoot, ver)
		return goRoot, nil
	}

	if err := downloadCosmoToolchain(dlURL, cosmoCache, key); err != nil {
		return "", fmt.Errorf("failed to download the gosmopolitan toolchain from %s: %w", dlURL, err)
	}

	ver, err := cosmoGoVersionFunc(goRoot)
	if err != nil {
		return "", fmt.Errorf("downloaded gosmopolitan toolchain at %s failed its version probe: %w", goRoot, err)
	}
	logger.Info("cosmo-bootstrap: using %s from %s (%s)", key, goRoot, ver)
	return goRoot, nil
}

// cosmoReleasePattern matches ResolveCosmoVersion's real-release shape, not its branch-key fallback.
var cosmoReleasePattern = regexp.MustCompile(`^v[0-9]`)

// ResolveCosmoVersion answers which buildhost release this host would build
// against, without downloading it. It returns the branch key when the probe
// cannot reach buildhost, because that is what the bootstrap would then use.
func ResolveCosmoVersion() string {
	hostOS, hostArch := cosmoHostPlatformFunc()
	return cosmoCacheKey(cosmoDownloadURL(defaultCosmoBranch, hostOS, hostArch), defaultCosmoBranch)
}

// cosmoGoBinPath names the fork's go binary in a GOROOT, for the HOST
// platform. Spell it by hand and an NT path misses the cache and cannot exec.
func cosmoGoBinPath(root string) string {
	goBin := filepath.Join(root, "bin", "go")
	if hostOS, _ := cosmoHostPlatformFunc(); hostOS == "windows" {
		goBin += ".exe"
	}
	return goBin
}

// cosmoGoVersion runs the toolchain's own `go version` as a health probe and
// returns its (trimmed) output.
func cosmoGoVersion(root string) (string, error) {
	cmd := exec.Command(cosmoGoBinPath(root), "version")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOROOT="+root)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go version failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// cosmoDownloadURL names the buildhost slot for a branch's latest release.
func cosmoDownloadURL(branch, goos, goarch string) string {
	return fmt.Sprintf("%s?branch=%s&os=%s&arch=%s", cosmoDownloadBase, url.QueryEscape(branch), goos, goarch)
}

// cosmoCacheKey derives the cache dir: redirect version (v<N>) if probeable, else the branch
// name. Shared with the dats bootstrap -- keep compatible.
func cosmoCacheKey(dlURL, branch string) string {
	if v := probeCosmoVersion(dlURL); v != "" {
		return "v" + v
	}
	return "branch-" + sanitizeCacheKey(branch)
}

// probeCosmoVersion answers the release a buildhost dl endpoint redirects to,
// or "" for a caller that keys on the branch either way.
func probeCosmoVersion(dlURL string) string {
	v, _ := probeCosmoRelease(dlURL)
	return v
}

// probeCosmoRelease is the same probe, with the reason it came back empty: a
// slot naming no release is the other repository's publishing, and an
// unreachable buildhost is a retry.
func probeCosmoRelease(dlURL string) (string, error) {
	client := &http.Client{
		Timeout: cosmoProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Head(dlURL)
	if err != nil {
		return "", fmt.Errorf("asking %s: %w", dlURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		// A 5xx is buildhost failing to answer. Every other status is an
		// answer, and the answer is that this slot names no release.
		if resp.StatusCode >= 500 {
			return "", fmt.Errorf("asking %s: HTTP %d", dlURL, resp.StatusCode)
		}
		return "", nil
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return "", fmt.Errorf("parsing the redirect from %s: %w", dlURL, err)
	}
	v := loc.Query().Get("v")
	if v == "" {
		return "", nil
	}
	return sanitizeCacheKey(v), nil
}

// resolveCosmoReleaseStrict answers which gosmopolitan release this host
// builds against, for a caller that must not accept a branch key. It retries
// an unreachable buildhost rather than turning a bad moment into a claim
// about what buildhost published.
func resolveCosmoReleaseStrict() (string, error) {
	hostOS, hostArch := cosmoHostPlatformFunc()
	dlURL := cosmoDownloadURL(defaultCosmoBranch, hostOS, hostArch)

	var lastErr error
	for attempt := range cosmoProbeAttempts {
		if attempt > 0 {
			cosmoProbeSleep(cosmoProbeRetryDelay)
		}
		v, err := probeCosmoRelease(dlURL)
		if err != nil {
			lastErr = err
			logger.Warn("cosmo: buildhost did not answer (attempt %d): %v", attempt+1, err)
			continue
		}
		if v == "" {
			// An answer, so retrying cannot change it: this slot has no release.
			return "branch-" + sanitizeCacheKey(defaultCosmoBranch), nil
		}
		return "v" + v, nil
	}
	return "", lastErr
}

// sanitizeCacheKey replaces every character outside the ASCII letters, digits, dot, underscore and hyphen with '-'
// so branch names and version strings are safe directory names.
func sanitizeCacheKey(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '.', r == '_', r == '-':
			return r
		default:
			return '-'
		}
	}, s)
}

// downloadCosmoToolchain fetches the gosmopolitan tarball (top-level dir go/)
// and extracts it to <cosmoCache>/<key>/go. The extraction happens in a temp
// directory that is renamed into place only when complete, so an interrupted
// download never poisons the cache.
func downloadCosmoToolchain(dlURL, cosmoCache, key string) error {
	if err := os.MkdirAll(cosmoCache, 0755); err != nil {
		return err
	}

	// Mid-line progress fragment, completed below; bypasses the logger via rawStderr (see logging.go).
	fmt.Fprintf(rawStderr, "cosmo-bootstrap: downloading %s", dlURL)
	dlStart := time.Now()

	tmpDir, err := os.MkdirTemp(cosmoCache, ".extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// A reset mid-stream is the network, not an answer, so it is retried on a
	// fixed cadence rather than failing the build. Depth: docs/CI.md
	for attempt := 1; ; attempt++ {
		err := fetchCosmoInto(dlURL, tmpDir)
		if err == nil {
			break
		}
		var terminal terminalDownloadError
		if errors.As(err, &terminal) {
			fmt.Fprintf(rawStderr, "\n")
			return err
		}
		fmt.Fprintf(rawStderr, "\n")
		logger.Warn("⇒ Warning: the gosmopolitan download failed (attempt %d): %v -- retrying in %s", attempt, err, cosmoRetryInterval)
		if err := clearDir(tmpDir); err != nil {
			return err
		}
		time.Sleep(cosmoRetryInterval)
		fmt.Fprintf(rawStderr, "cosmo-bootstrap: downloading %s", dlURL)
	}
	fmt.Fprintf(rawStderr, " %s\n", fmtDuration(time.Since(dlStart)))

	goBin := cosmoGoBinPath(filepath.Join(tmpDir, "go"))
	if _, err := os.Stat(goBin); err != nil {
		return fmt.Errorf("downloaded archive does not contain go/bin/%s: %w", filepath.Base(goBin), err)
	}

	dest := filepath.Join(cosmoCache, key)
	// A leftover dir under this key has no usable bin/go (the caller checked
	// before downloading); clear it so the rename can land.
	if err := os.RemoveAll(dest); err != nil {
		return err
	}
	if err := os.Rename(tmpDir, dest); err != nil {
		return fmt.Errorf("rename failed: %w", err)
	}
	return nil
}

// Fixed, uncapped: a give-up leaves the build with no compiler.
var cosmoRetryInterval = 3 * time.Second

// An answer rather than a network fault, so the retry stops.
type terminalDownloadError struct{ error }

// fetchCosmoInto downloads the tarball once and extracts it into dir.
func fetchCosmoInto(dlURL, dir string) error {
	client := &http.Client{Timeout: cosmoDownloadTimeout}
	resp, err := client.Get(dlURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return terminalDownloadError{fmt.Errorf("HTTP 404: buildhost publishes no gosmopolitan toolchain for this host yet")}
	}
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		return terminalDownloadError{fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := extractTarGz(resp.Body, dir); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	return nil
}

// clearDir empties dir, so a retry extracts into a clean directory.
func clearDir(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o755)
}
