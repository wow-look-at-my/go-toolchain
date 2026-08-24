package build

import "os"

// inDockerCheck detects Docker containers; a variable so tests can override it.
var inDockerCheck = defaultInDockerCheck

func defaultInDockerCheck() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// InDocker returns true when the process is running inside a Docker container.
func InDocker() bool {
	return inDockerCheck()
}

// SetInDockerCheck overrides the Docker detection function (for testing).
func SetInDockerCheck(f func() bool) func() {
	old := inDockerCheck
	inDockerCheck = f
	return func() { inDockerCheck = old }
}

// BinaryName returns name_goos_goarch, with .exe appended on Windows.
//
// Wasm (GOARCH=wasm) swaps to name_wasm_<goos>, no extension: buildhost's
// publish action parses os/arch from the trailing two underscore tokens
// after stripping .exe, so this form publishes as os=wasm/arch=<goos>; any
// extension excludes it from the upload set. UnpublishableWasmName is the opt-out.
func BinaryName(name, goos, goarch string) string {
	// The cosmo fat APE is the plain name. One file runs on every platform in
	// the set, so a platform suffix would name a property it does not have,
	// and build/<name> is what a consumer runs.
	if goos == "cosmo" {
		return name
	}
	if goarch == "wasm" {
		return name + "_wasm_" + goos
	}
	out := name + "_" + goos + "_" + goarch
	if goos == "windows" {
		out += ".exe"
	}
	return out
}

// UnpublishableWasmName returns the opt-out wasm artifact name used when
// buildhost publishing of wasm artifacts is disabled
// (GO_TOOLCHAIN_WASM_PUBLISH=0): name_<goos>_wasm.wasm. The .wasm suffix
// cannot match the buildhost-publish action's <binary>_{os}_{arch} pattern
// (it strips only .exe, and the trailing token would contain a dot), so the
// artifact stays out of the publish upload set — for consumers whose
// buildhost predates wasm artifact support, where an os=wasm upload 400s
// and aborts the whole publish.
func UnpublishableWasmName(name, goos string) string {
	return name + "_" + goos + "_wasm.wasm"
}
