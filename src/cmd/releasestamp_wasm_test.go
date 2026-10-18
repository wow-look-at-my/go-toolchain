package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// buildRealWasm links a main package for GOOS=goos GOARCH=wasm with the go command on PATH, with no release variables set.
func buildRealWasm(t *testing.T, goos string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/stampprobe\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println(\"hi\") }\n"), 0o644))
	out := filepath.Join(dir, "stampprobe_wasm_"+goos)
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, gosmoReleaseEnv+"=") || strings.HasPrefix(kv, gosmoReleaseRevisionEnv+"=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "GOOS="+goos, "GOARCH=wasm", "CGO_ENABLED=0", "GOFLAGS=")
	built, err := cmd.CombinedOutput()
	require.NoError(t, err, "GOOS=%s GOARCH=wasm go build: %s", goos, built)
	return out
}

// The stamp check reads what the real wasm linker writes, for js and for wasip1.
// The linker writes no build-info header there, so a read that needs one fails every wasm release build.
func TestVerifyReleaseStampReadsARealWasmBuild(t *testing.T) {
	t.Serial()
	t.Setenv(gosmoReleaseEnv, "412")
	t.Setenv(gosmoReleaseRevisionEnv, "abc")
	for _, goos := range []string{"js", "wasip1"} {
		t.Run(goos, func(t *testing.T) {
			path := buildRealWasm(t, goos)
			require.True(t, hasMagic(path, wasmMagic), "GOOS=%s output must be a WebAssembly module", goos)

			settings, err := rawBuildInfoSettings(path)
			require.NoError(t, err)
			assert.Equal(t, goos, settings["GOOS"])
			assert.Equal(t, "wasm", settings["GOARCH"])

			// Built with no release variables: the check must read the module and name the missing stamp.
			assert.ErrorContains(t, verifyReleaseStamp(runner.NewMock(), stampJob(), path), "did not stamp the release")

			// The release settings the fork adds sit in the same modinfo, ahead of its closing sentinel.
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			end := bytes.Index(data, []byte(modinfoEnd))
			require.GreaterOrEqual(t, end, 0, "GOOS=%s module must carry the modinfo sentinels", goos)
			stamped := append(append(append([]byte{}, data[:end]...), "build\trelease=412\nbuild\trelease.revision=abc\n"...), data[end:]...)
			stampedPath := filepath.Join(t.TempDir(), "stamped_wasm_"+goos)
			require.NoError(t, os.WriteFile(stampedPath, stamped, 0o755))
			require.NoError(t, verifyReleaseStamp(runner.NewMock(), stampJob(), stampedPath))
		})
	}
}
