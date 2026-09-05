package cmd

import (
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestScratchBase: git reads the scratch path from its own argument list,
// where nothing translates it, so an NT host cannot be handed cosmo's /tmp.
func TestScratchBase(t *testing.T) {
	t.Serial()
	assert.Equal(t, "", scratchBase("linux"))
	assert.Equal(t, "", scratchBase("darwin"))

	old := goCacheDirFunc
	defer func() { goCacheDirFunc = old }()

	goCacheDirFunc = func() (string, error) { return `C:\Users\r\.cache\go-toolchain`, nil }
	assert.Equal(t, `C:\Users\r\.cache\go-toolchain`, scratchBase("windows"))

	goCacheDirFunc = func() (string, error) { return "", errors.New("no cache dir") }
	assert.Equal(t, "", scratchBase("windows"), "an unknown cache directory falls back to the ambient one")
}

// TestArgListTempDir: the go command opens -coverprofile and
// -debug-actiongraph by the spelling it is handed. smoke-windows died on
// "open D:\...\tmp\go-toolchain-cov\coverage-N.out: The system cannot find the
// path specified", which is what cosmo's /tmp becomes to a native go.exe.
func TestArgListTempDir(t *testing.T) {
	t.Serial()
	old := goCacheDirFunc
	defer func() { goCacheDirFunc = old }()
	goCacheDirFunc = func() (string, error) { return `C:\Users\r\.cache\go-toolchain`, nil }

	assert.Equal(t, `C:\Users\r\.cache\go-toolchain`, argListTempDir("windows"))
	assert.Equal(t, os.TempDir(), argListTempDir("linux"), "every other host keeps the ambient temp directory")
	assert.Equal(t, os.TempDir(), argListTempDir("darwin"))

	goCacheDirFunc = func() (string, error) { return "", errors.New("no cache dir") }
	assert.Equal(t, os.TempDir(), argListTempDir("windows"), "an unknown cache directory still answers a usable path")
}
