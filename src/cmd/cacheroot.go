package cmd

import (
	"fmt"
	"os"
	"path/filepath"
)

// userCacheRoot answers the XDG cache root on every host: $XDG_CACHE_HOME when
// it holds an absolute path, else $HOME/.cache.
//
// Never reach for os.UserCacheDir here. It answers a per-platform directory,
// such as ~/Library/Caches, that this repo, its docs and CI never look in.
func userCacheRoot() (string, error) {
	// A relative XDG_CACHE_HOME is invalid per the XDG spec, and is ignored.
	if dir := os.Getenv("XDG_CACHE_HOME"); filepath.IsAbs(dir) {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine cache directory: %w", err)
	}
	return filepath.Join(home, ".cache"), nil
}
