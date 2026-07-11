package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-toolchain/src/hostos"
)

var setupCGOOnce sync.Once

// setupCGOEnvironment ensures PKG_CONFIG_PATH includes directories where
// C libraries may be installed. It checks (in order):
//  1. The go-toolchain opencv cache (~/.cache/go-toolchain/opencv-*)
//  2. Homebrew on macOS (/opt/homebrew/lib/pkgconfig)
//
// This runs once per invocation when --cgo is enabled.
func setupCGOEnvironment() {
	if !cgoEnabled {
		return
	}
	setupCGOOnce.Do(func() {
		// Check cached opencv builds from ensure_opencv (go:generate tool)
		if dir, err := cachedOpenCVPkgConfig(); err == nil {
			addPkgConfigPath(dir)
		}

		// On macOS, also add homebrew's pkgconfig for other C deps
		// (hostos, not runtime: a cosmo fat APE on a mac must still find brew)
		if hostos.GOOS() == "darwin" {
			if prefix, err := brewPrefix(); err == nil {
				pkgConfigDir := filepath.Join(prefix, "lib", "pkgconfig")
				if _, err := os.Stat(pkgConfigDir); err == nil {
					addPkgConfigPath(pkgConfigDir)
				}
			}
		}
	})
}

// cachedOpenCVPkgConfig looks for a cached opencv build in the go-toolchain
// cache directory and returns the pkgconfig path if found.
func cachedOpenCVPkgConfig() (string, error) {
	cacheDir, err := goCacheDirFunc()
	if err != nil {
		return "", err
	}

	// Scan for opencv-* directories
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "opencv-") {
			continue
		}
		// Check lib/pkgconfig and lib64/pkgconfig
		for _, libDir := range []string{"lib", "lib64"} {
			pcDir := filepath.Join(cacheDir, e.Name(), libDir, "pkgconfig")
			pcFile := filepath.Join(pcDir, "opencv4.pc")
			if _, err := os.Stat(pcFile); err == nil {
				return pcDir, nil
			}
		}
	}
	return "", fmt.Errorf("no cached opencv found")
}

func addPkgConfigPath(dir string) {
	existing := os.Getenv("PKG_CONFIG_PATH")
	if strings.Contains(existing, dir) {
		return
	}
	if existing != "" {
		os.Setenv("PKG_CONFIG_PATH", dir+":"+existing)
	} else {
		os.Setenv("PKG_CONFIG_PATH", dir)
	}
	fmt.Fprintf(os.Stderr, "cgo: added %s to PKG_CONFIG_PATH\n", dir)
}

func brewPrefix() (string, error) {
	out, err := exec.Command("brew", "--prefix").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
