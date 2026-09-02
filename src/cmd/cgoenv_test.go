package cmd

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAddPkgConfigPath_Empty(t *testing.T) {
	old := os.Getenv("PKG_CONFIG_PATH")
	defer os.Setenv("PKG_CONFIG_PATH", old)

	os.Setenv("PKG_CONFIG_PATH", "")
	addPkgConfigPath("/usr/lib/pkgconfig")
	assert.Equal(t, "/usr/lib/pkgconfig", os.Getenv("PKG_CONFIG_PATH"))
}

func TestAddPkgConfigPath_Existing(t *testing.T) {
	old := os.Getenv("PKG_CONFIG_PATH")
	defer os.Setenv("PKG_CONFIG_PATH", old)

	os.Setenv("PKG_CONFIG_PATH", "/existing/path")
	addPkgConfigPath("/new/path")
	assert.Equal(t, "/new/path:/existing/path", os.Getenv("PKG_CONFIG_PATH"))
}

func TestAddPkgConfigPath_AlreadyPresent(t *testing.T) {
	old := os.Getenv("PKG_CONFIG_PATH")
	defer os.Setenv("PKG_CONFIG_PATH", old)

	os.Setenv("PKG_CONFIG_PATH", "/some/path:/other/path")
	addPkgConfigPath("/some/path")
	// Should not duplicate
	assert.Equal(t, "/some/path:/other/path", os.Getenv("PKG_CONFIG_PATH"))
}

func TestCachedOpenCVPkgConfig_NoCache(t *testing.T) {
	// Not parallel: goCacheDirFunc is a package global, and a sibling's assignment would win.
	dir := t.TempDir()
	oldFunc := goCacheDirFunc
	goCacheDirFunc = func() (string, error) { return dir, nil }
	defer func() { goCacheDirFunc = oldFunc }()

	_, err := cachedOpenCVPkgConfig()
	assert.Error(t, err)
}

func TestCachedOpenCVPkgConfig_Found(t *testing.T) {
	// Not parallel: goCacheDirFunc is a package global, and a sibling's assignment would win.
	dir := t.TempDir()
	oldFunc := goCacheDirFunc
	goCacheDirFunc = func() (string, error) { return dir, nil }
	defer func() { goCacheDirFunc = oldFunc }()

	// Create opencv cache structure
	pcDir := filepath.Join(dir, "opencv-4.9.0", "lib", "pkgconfig")
	require.NoError(t, os.MkdirAll(pcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pcDir, "opencv4.pc"), []byte("prefix=/usr"), 0644))

	result, err := cachedOpenCVPkgConfig()
	require.NoError(t, err)
	assert.Equal(t, pcDir, result)
}

func TestCachedOpenCVPkgConfig_FoundInLib64(t *testing.T) {
	// Not parallel: goCacheDirFunc is a package global, and a sibling's assignment would win.
	dir := t.TempDir()
	oldFunc := goCacheDirFunc
	goCacheDirFunc = func() (string, error) { return dir, nil }
	defer func() { goCacheDirFunc = oldFunc }()

	// Create opencv cache in lib64
	pcDir := filepath.Join(dir, "opencv-4.9.0", "lib64", "pkgconfig")
	require.NoError(t, os.MkdirAll(pcDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(pcDir, "opencv4.pc"), []byte("prefix=/usr"), 0644))

	result, err := cachedOpenCVPkgConfig()
	require.NoError(t, err)
	assert.Equal(t, pcDir, result)
}

func TestSetupCGOEnvironment_Disabled(t *testing.T) {
	oldCGO := cgoEnabled
	cgoEnabled = false
	defer func() { cgoEnabled = oldCGO }()

	// Reset sync.Once so it can run
	setupCGOOnce = sync.Once{}

	old := os.Getenv("PKG_CONFIG_PATH")
	defer os.Setenv("PKG_CONFIG_PATH", old)
	os.Setenv("PKG_CONFIG_PATH", "")

	setupCGOEnvironment()
	// PKG_CONFIG_PATH should not change
	assert.Equal(t, "", os.Getenv("PKG_CONFIG_PATH"))
}
