package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

var setupCGOOnce sync.Once

// setupCGOEnvironment detects homebrew on macOS and adds its pkg-config
// paths to PKG_CONFIG_PATH so CGO packages that depend on C libraries
// (e.g. opencv via gocv) can find them without manual configuration.
func setupCGOEnvironment() {
	if !cgoEnabled {
		return
	}
	setupCGOOnce.Do(func() {
		if runtime.GOOS != "darwin" {
			return
		}
		prefix, err := brewPrefix()
		if err != nil {
			return
		}
		pkgConfigDir := filepath.Join(prefix, "lib", "pkgconfig")
		if _, err := os.Stat(pkgConfigDir); err != nil {
			return
		}
		existing := os.Getenv("PKG_CONFIG_PATH")
		if strings.Contains(existing, pkgConfigDir) {
			return
		}
		if existing != "" {
			os.Setenv("PKG_CONFIG_PATH", pkgConfigDir+":"+existing)
		} else {
			os.Setenv("PKG_CONFIG_PATH", pkgConfigDir)
		}
		fmt.Fprintf(os.Stderr, "cgo: added %s to PKG_CONFIG_PATH\n", pkgConfigDir)
	})
}

func brewPrefix() (string, error) {
	out, err := exec.Command("brew", "--prefix").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
