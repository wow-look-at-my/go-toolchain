package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestCacheHome_Default(t *testing.T) {
	old := os.Getenv("XDG_CACHE_HOME")
	os.Unsetenv("XDG_CACHE_HOME")
	defer os.Setenv("XDG_CACHE_HOME", old)

	home, _ := os.UserHomeDir()
	assert.Equal(t, filepath.Join(home, ".cache", "go-toolchain"), cacheHome())
}

func TestCacheHome_XDG(t *testing.T) {
	old := os.Getenv("XDG_CACHE_HOME")
	os.Setenv("XDG_CACHE_HOME", "/custom/cache")
	defer os.Setenv("XDG_CACHE_HOME", old)

	assert.Equal(t, "/custom/cache/go-toolchain", cacheHome())
}

func TestIsVanityHostReachableWithChecker(t *testing.T) {
	old := vanityHostChecker
	defer func() { vanityHostChecker = old }()

	vanityHostChecker = func(host string) bool {
		return host == "reachable.example.com"
	}

	assert.True(t, isVanityHostReachable("reachable.example.com"))
	assert.False(t, isVanityHostReachable("unreachable.example.com"))
}
