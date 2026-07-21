package cmd

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Environment variables controlling gosmopolitan toolchain resolution.
const (
	// cosmoGorootEnv points at a local gosmopolitan build's GOROOT (the
	// directory containing bin/go). When set, no download happens.
	cosmoGorootEnv = "GO_TOOLCHAIN_COSMO_GOROOT"
	// cosmoBranchEnv selects which buildhost branch the gosmopolitan
	// toolchain tarball is downloaded from. Default: master.
	cosmoBranchEnv = "GO_TOOLCHAIN_COSMO_BRANCH"
)

const (
	defaultCosmoBranch   = "master"
	cosmoProbeTimeout    = 30 * time.Second
	cosmoDownloadTimeout = 10 * time.Minute
)

// Test seams — overridden in tests to avoid real downloads and version probes.
var (
	ensureCosmoToolchainFunc = EnsureCosmoToolchain
	cosmoDownloadBase        = "https://dl.pazer.build/gosmopolitan"
	cosmoHostPlatformFunc    = cosmoHostPlatform
	cosmoGoVersionFunc       = cosmoGoVersion
)

// cosmoHostPlatform returns the platform whose gosmopolitan tarball can run
// here: the HOST os (a cosmo fat APE reports runtime.GOOS=="cosmo" on every
// host) plus runtime.GOARCH (always the host arch, even inside a fat APE).
func cosmoHostPlatform() (goos, goarch string) {
	return hostos.GOOS(), runtime.GOARCH
}

// EnsureCosmoToolchain resolves a gosmopolitan Go toolchain (the Go fork that
// builds GOOS=cosmo fat APEs) and returns its GOROOT. Resolution order:
//
//  1. GO_TOOLCHAIN_COSMO_GOROOT — a local build's GOROOT, used directly.
//  2. Download from buildhost (dl.pazer.build/gosmopolitan, branch selected
//     by GO_TOOLCHAIN_COSMO_BRANCH, default master) and cache it under the
//     same cache root the Go bootstrap uses (~/.cache/go-toolchain/cosmo/).
//
// The cache is keyed by the buildhost release version parsed from the dl
// endpoint's redirect (v<N>), so a cached toolchain is never re-downloaded
// and a new buildhost release is picked up automatically. If the redirect
// cannot be parsed, the cache falls back to a branch-keyed directory that is
// downloaded once and then reused as long as it exists.
func EnsureCosmoToolchain() (string, error) {
	if root := os.Getenv(cosmoGorootEnv); root != "" {
		return useLocalCosmoGoroot(root)
	}

	hostOS, hostArch := cosmoHostPlatformFunc()
	if hostOS != "linux" || hostArch != "amd64" {
		return "", fmt.Errorf("no prebuilt gosmopolitan toolchain is published for %s/%s hosts (only linux/amd64 today); set %s to a local gosmopolitan build's GOROOT", hostOS, hostArch, cosmoGorootEnv)
	}

	branch := envOr(cosmoBranchEnv, defaultCosmoBranch)
	dlURL := fmt.Sprintf("%s?branch=%s&os=%s&arch=%s", cosmoDownloadBase, url.QueryEscape(branch), hostOS, hostArch)

	cacheDir, err := goCacheDirFunc()
	if err != nil {
		return "", err
	}
	cosmoCache := filepath.Join(cacheDir, "cosmo")

	key := cosmoCacheKey(dlURL, branch)
	goRoot := filepath.Join(cosmoCache, key, "go")
	if _, statErr := os.Stat(filepath.Join(goRoot, "bin", "go")); statErr == nil {
		ver, verErr := cosmoGoVersionFunc(goRoot)
		if verErr != nil {
			return "", fmt.Errorf("cached gosmopolitan toolchain at %s is broken: %w (delete it to re-download, or set %s to a local build)", goRoot, verErr, cosmoGorootEnv)
		}
		logger.Info("cosmo-bootstrap: using cached %s from %s (%s)", key, goRoot, ver)
		return goRoot, nil
	}

	if err := downloadCosmoToolchain(dlURL, cosmoCache, key); err != nil {
		return "", fmt.Errorf("failed to download the gosmopolitan toolchain from %s: %w (set %s to use a local build, or %s to pick a different buildhost branch)", dlURL, err, cosmoGorootEnv, cosmoBranchEnv)
	}

	ver, err := cosmoGoVersionFunc(goRoot)
	if err != nil {
		return "", fmt.Errorf("downloaded gosmopolitan toolchain at %s failed its version probe: %w", goRoot, err)
	}
	logger.Info("cosmo-bootstrap: using %s from %s (%s)", key, goRoot, ver)
	return goRoot, nil
}

// useLocalCosmoGoroot validates and returns the GOROOT named by
// GO_TOOLCHAIN_COSMO_GOROOT.
func useLocalCosmoGoroot(root string) (string, error) {
	if _, err := os.Stat(cosmoGoBinPath(root)); err != nil {
		return "", fmt.Errorf("%s=%s does not look like a gosmopolitan GOROOT (no bin/go): %w", cosmoGorootEnv, root, err)
	}
	ver, err := cosmoGoVersionFunc(root)
	if err != nil {
		return "", fmt.Errorf("%s=%s: %w", cosmoGorootEnv, root, err)
	}
	logger.Info("cosmo-bootstrap: using %s=%s (%s)", cosmoGorootEnv, root, ver)
	return root, nil
}

// cosmoGoBinPath returns the go binary path inside a GOROOT, honoring the
// HOST platform's executable naming.
func cosmoGoBinPath(root string) string {
	goBin := filepath.Join(root, "bin", "go")
	if hostos.GOOS() == "windows" {
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

// cosmoCacheKey derives the cache directory name for a buildhost download.
// buildhost's dl endpoint redirects to a static URL whose query carries the
// resolved release version (v=<N>); that version makes a stable, collision
// free cache key (v<N>). When the redirect cannot be probed or parsed, the
// key falls back to the branch name: that copy is downloaded once and then
// reused as long as it exists (no staleness detection) rather than paying a
// large download on every run. Shared keying: both the gosmopolitan
// toolchain bootstrap (this file) and the dats bootstrap (datsbootstrap.go)
// key their caches with it — keep changes compatible with both.
func cosmoCacheKey(dlURL, branch string) string {
	if v := probeCosmoVersion(dlURL); v != "" {
		return "v" + v
	}
	return "branch-" + sanitizeCacheKey(branch)
}

// probeCosmoVersion issues one redirect-stopping HEAD request against the dl
// endpoint and extracts the release version from the Location's v query
// parameter. Any failure returns "" (callers fall back to branch keying).
// Used for every buildhost dl endpoint, not just gosmopolitan — the dats
// bootstrap probes through it too (via cosmoCacheKey).
func probeCosmoVersion(dlURL string) string {
	client := &http.Client{
		Timeout: cosmoProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Head(dlURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return ""
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		return ""
	}
	v := loc.Query().Get("v")
	if v == "" {
		return ""
	}
	return sanitizeCacheKey(v)
}

// sanitizeCacheKey replaces every character outside [A-Za-z0-9._-] with '-'
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

	// Mid-line progress fragment (completed below on the same line):
	// bypasses the logger via rawStderr, see logging.go.
	fmt.Fprintf(rawStderr, "cosmo-bootstrap: downloading %s", dlURL)
	dlStart := time.Now()
	client := &http.Client{Timeout: cosmoDownloadTimeout}
	resp, err := client.Get(dlURL)
	if err != nil {
		fmt.Fprintf(rawStderr, "\n")
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(rawStderr, "\n")
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmpDir, err := os.MkdirTemp(cosmoCache, ".extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	if err := extractTarGz(resp.Body, tmpDir); err != nil {
		return fmt.Errorf("extraction failed: %w", err)
	}
	fmt.Fprintf(rawStderr, " %s\n", fmtDuration(time.Since(dlStart)))

	if _, err := os.Stat(filepath.Join(tmpDir, "go", "bin", "go")); err != nil {
		return fmt.Errorf("downloaded archive does not contain go/bin/go: %w", err)
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
