// Package wasmexec carries the fork's lib/wasm/wasm_exec.js, the JS harness a
// GOOS=js binary runs under. A build of this pipeline refreshes the copy
// from the fork checkout, and a copy that differs from it is a dirty tree.
package wasmexec

import _ "embed"

// Script is the harness, byte for byte the fork's.
//
//go:embed wasm_exec.js
var Script []byte
