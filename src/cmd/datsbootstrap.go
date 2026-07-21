package cmd

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Environment variables controlling dats resolution.
const (
	// datsBinEnv points at a local dats binary. When set, no download
	// happens (the binary is only health-probed with `dats version`).
	datsBinEnv = "GO_TOOLCHAIN_DATS_BIN"
	// datsBranchEnv selects which buildhost branch the dats binary is
	// downloaded from. Default: master (dats' default branch).
	datsBranchEnv = "GO_TOOLCHAIN_DATS_BRANCH"
)

const defaultDatsBranch = "master"

// Test seams — overridden in tests to avoid real downloads and version probes.
// The host-platform rule is the same one the cosmo bootstrap uses (hostos +
// runtime.GOARCH); dats publishes native binaries for every os/arch pair, so
// unlike cosmo there is no host gate.
var (
	ensureDatsFunc       = EnsureDats
	datsDownloadBase     = "https://dl.pazer.build/dats"
	datsHostPlatformFunc = cosmoHostPlatform
	datsVersionFunc      = datsVersion
)

// EnsureDats resolves a dats binary (the declarative CLI test runner,
// github.com/wow-look-at-my/dats) and returns its path. Resolution order:
//
//  1. GO_TOOLCHAIN_DATS_BIN — a local binary, used directly.
//  2. Download from buildhost (dl.pazer.build/dats, branch selected by
//     GO_TOOLCHAIN_DATS_BRANCH, default master) and cache it under the same
//     cache root the Go bootstrap uses (~/.cache/go-toolchain/dats/).
//
// The cache is keyed by the buildhost release version parsed from the dl
// endpoint's redirect (v<N>), so a cached binary is never re-downloaded and a
// new buildhost release is picked up automatically. If the redirect cannot be
// parsed, the cache falls back to a branch-keyed directory that is downloaded
// once and then reused as long as it exists.
func EnsureDats() (string, error) {
	if bin := os.Getenv(datsBinEnv); bin != "" {
		return useLocalDats(bin)
	}

	hostOS, hostArch := datsHostPlatformFunc()
	branch := envOr(datsBranchEnv, defaultDatsBranch)
	dlURL := fmt.Sprintf("%s?branch=%s&os=%s&arch=%s", datsDownloadBase, url.QueryEscape(branch), hostOS, hostArch)

	cacheDir, err := goCacheDirFunc()
	if err != nil {
		return "", err
	}
	datsCache := filepath.Join(cacheDir, "dats")

	// cosmoCacheKey is generic buildhost keying (redirect version probe with
	// a branch-keyed fallback) — reused rather than duplicated.
	key := cosmoCacheKey(dlURL, branch)
	binPath := filepath.Join(datsCache, key, datsArtifactName("dats", hostOS))
	if _, statErr := os.Stat(binPath); statErr == nil {
		ver, verErr := datsVersionFunc(binPath)
		if verErr != nil {
			return "", fmt.Errorf("cached dats at %s is broken: %w (delete it to re-download, or set %s to a local binary)", binPath, verErr, datsBinEnv)
		}
		logger.Info("dats-bootstrap: using cached %s from %s (%s)", key, binPath, ver)
		return binPath, nil
	}

	if err := downloadDats(dlURL, binPath); err != nil {
		return "", fmt.Errorf("failed to download dats from %s: %w (set %s to use a local binary, or %s to pick a different buildhost branch)", dlURL, err, datsBinEnv, datsBranchEnv)
	}

	ver, err := datsVersionFunc(binPath)
	if err != nil {
		return "", fmt.Errorf("downloaded dats at %s failed its version probe: %w", binPath, err)
	}
	logger.Info("dats-bootstrap: using %s from %s (%s)", key, binPath, ver)
	return binPath, nil
}

// useLocalDats validates and returns the binary named by GO_TOOLCHAIN_DATS_BIN.
func useLocalDats(bin string) (string, error) {
	ver, err := datsVersionFunc(bin)
	if err != nil {
		return "", fmt.Errorf("%s=%s: %w", datsBinEnv, bin, err)
	}
	logger.Info("dats-bootstrap: using %s=%s (%s)", datsBinEnv, bin, ver)
	return bin, nil
}

// datsVersion runs `dats version` as a health probe and returns its (trimmed)
// output.
func datsVersion(bin string) (string, error) {
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		return "", fmt.Errorf("dats version failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// downloadDats fetches the dats binary — a single raw native executable, not
// an archive — to binPath. The bytes stream to a temp file in the destination
// directory, get the executable bit, and are atomically renamed into place, so
// an interrupted download never poisons the cache.
func downloadDats(dlURL, binPath string) error {
	if err := os.MkdirAll(filepath.Dir(binPath), 0755); err != nil {
		return err
	}

	// Mid-line progress fragment (completed below on the same line):
	// bypasses the logger via rawStderr, see logging.go.
	fmt.Fprintf(rawStderr, "dats-bootstrap: downloading %s", dlURL)
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

	tmp, err := os.CreateTemp(filepath.Dir(binPath), ".tmp-")
	if err != nil {
		fmt.Fprintf(rawStderr, "\n")
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed into place
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		fmt.Fprintf(rawStderr, "\n")
		return err
	}
	if err := tmp.Close(); err != nil {
		fmt.Fprintf(rawStderr, "\n")
		return err
	}
	fmt.Fprintf(rawStderr, " %s\n", fmtDuration(time.Since(dlStart)))

	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), binPath)
}
