package cmd

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// slopfix commits none of its generated parse tables, so no module can
// import the rule. The published binary is what its README hands a consumer.
const (
	// slopfixBinEnv points the phase at a local build instead of buildhost.
	slopfixBinEnv = "GO_TOOLCHAIN_SLOPFIX_BIN"
	// slopfixVersionEnv pins the buildhost release.
	slopfixVersionEnv = "GO_TOOLCHAIN_SLOPFIX_VERSION"

	slopfixDownloadTimeout = 5 * time.Minute
)

// Test seams, so a test drives the resolution without a download.
var (
	slopfixDownloadBase = "https://dl.pazer.build/slopfix"
	ensureSlopfixFunc   = ensureSlopfix
)

// slopfixDownloadURL names the buildhost slot a pin selects.
func slopfixDownloadURL(pin, goos, goarch string) string {
	if pin == "" {
		return fmt.Sprintf("%s?os=%s&arch=%s", slopfixDownloadBase, goos, goarch)
	}
	return fmt.Sprintf("%s?v=%s&os=%s&arch=%s", slopfixDownloadBase, pin, goos, goarch)
}

// ensureSlopfix answers the path of a runnable slopfix, downloading it when
// the cache holds none. A failure here fails the phase: a comment scan that
// cannot read a comment must not report a clean tree.
func ensureSlopfix() (string, error) {
	if bin := os.Getenv(slopfixBinEnv); bin != "" {
		if _, err := os.Stat(bin); err != nil {
			return "", fmt.Errorf("%s names %s, which is not there: %w", slopfixBinEnv, bin, err)
		}
		return bin, nil
	}

	hostOS, hostArch := cosmoHostPlatformFunc()
	pin := os.Getenv(slopfixVersionEnv)
	dlURL := slopfixDownloadURL(pin, hostOS, hostArch)

	cacheDir, err := goCacheDirFunc()
	if err != nil {
		return "", err
	}
	key := sanitizeCacheKey(pin)
	if key == "" {
		key = "latest"
	}
	dir := filepath.Join(cacheDir, "slopfix", key)
	bin := filepath.Join(dir, slopfixBinName(hostOS))
	if _, statErr := os.Stat(bin); statErr == nil {
		return bin, nil
	}

	if err := downloadSlopfix(dlURL, dir, bin); err != nil {
		return "", fmt.Errorf("failed to download slopfix from %s: %w (set %s to use a local build, or %s to pin one release)", dlURL, err, slopfixBinEnv, slopfixVersionEnv)
	}
	logger.Info("slopfix-bootstrap: using %s", bin)
	return bin, nil
}

// slopfixBinName carries the suffix NT needs and a posix host ignores.
func slopfixBinName(hostOS string) string {
	if hostOS == "windows" {
		return "slopfix.exe"
	}
	return "slopfix"
}

// downloadSlopfix writes the published binary, then moves it onto its cached
// name, so a killed download never leaves a half file that looks runnable.
func downloadSlopfix(dlURL, dir, bin string) error {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	client := &http.Client{Timeout: slopfixDownloadTimeout}
	resp, err := client.Get(dlURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("HTTP 404: buildhost publishes no slopfix for this host yet")
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp(dir, ".download-")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0755); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), bin)
}
