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
	// The cosmo fat APE is the plain name (one file covers every platform) and gets no .exe despite being a PE polyglot.
	assert.Equal(t, "myapp", BinaryName("myapp", "cosmo", "fat"))
	// WebAssembly targets use buildhost's os=wasm convention: name_wasm_<goos>, no extension (else upload skips it).
	assert.Equal(t, "myapp_wasm_js", BinaryName("myapp", "js", "wasm"))
	assert.Equal(t, "myapp_wasm_wasip1", BinaryName("myapp", "wasip1", "wasm"))
}

func TestUnpublishableWasmName(t *testing.T) {
	// The opt-out shape (GO_TOOLCHAIN_WASM_PUBLISH=0): .wasm suffix keeps the artifact out of the upload set.
	assert.Equal(t, "myapp_js_wasm.wasm", UnpublishableWasmName("myapp", "js"))
	assert.Equal(t, "myapp_wasip1_wasm.wasm", UnpublishableWasmName("myapp", "wasip1"))
}
