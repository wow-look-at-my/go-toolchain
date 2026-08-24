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
	// The cosmo fat APE is the plain name; a platform suffix would name a property it lacks, and build/<name> is what runs.
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

// Opt-out wasm name; its .wasm suffix can't match the publish pattern, so it stays out of the upload set.
func UnpublishableWasmName(name, goos string) string {
	return name + "_" + goos + "_wasm.wasm"
}
