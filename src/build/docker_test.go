package build

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInDockerReturnsFalseOutsideDocker(t *testing.T) {
	old := inDockerCheck
	defer func() { inDockerCheck = old }()

	inDockerCheck = func() bool { return false }
	assert.False(t, InDocker())
}

func TestInDockerReturnsTrueInsideDocker(t *testing.T) {
	old := inDockerCheck
	defer func() { inDockerCheck = old }()

	inDockerCheck = func() bool { return true }
	assert.True(t, InDocker())
}

func TestBinaryName(t *testing.T) {
	assert.Equal(t, "myapp_linux_amd64", BinaryName("myapp", "linux", "amd64"))
	assert.Equal(t, "myapp_darwin_arm64", BinaryName("myapp", "darwin", "arm64"))
	assert.Equal(t, "myapp_windows_amd64.exe", BinaryName("myapp", "windows", "amd64"))
	// The cosmo fat APE gets no .exe even though it is a genuine PE polyglot:
	// only the windows/* slot copies carry the .exe name.
	assert.Equal(t, "myapp_cosmo_fat", BinaryName("myapp", "cosmo", "fat"))
	// WebAssembly targets carry a .wasm suffix.
	assert.Equal(t, "myapp_js_wasm.wasm", BinaryName("myapp", "js", "wasm"))
	assert.Equal(t, "myapp_wasip1_wasm.wasm", BinaryName("myapp", "wasip1", "wasm"))
}
