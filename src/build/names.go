package build

// BinaryName returns name_goos_goarch, .exe appended on Windows.
//
// Wasm swaps to name_wasm_<goos>, no extension: publish parses os/arch
// from the trailing two underscore tokens after stripping .exe, so this
// publishes as os=wasm/arch=<goos>; an extension excludes it. See
// UnpublishableWasmName for the opt-out.
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
