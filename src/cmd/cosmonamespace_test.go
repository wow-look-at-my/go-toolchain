package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baseFakeGorootFiles is a minimal fork-toolchain GOROOT layout for
// fingerprint tests.
func baseFakeGorootFiles() map[string]string {
	return map[string]string{
		"VERSION":                      "go1.26.4cosmo",
		"bin/go":                       "go binary content",
		"bin/gofmt":                    "gofmt binary content",
		"pkg/tool/linux_amd64/compile": "compile binary content",
		"pkg/tool/linux_amd64/link":    "link binary content",
		"pkg/tool/linux_amd64/asm":     "asm binary content",
	}
}

func fingerprintGoroot(t *testing.T, files map[string]string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "goroot")
	writeFakeForkGoroot(t, root, files)
	ns, err := forkToolchainCacheNamespace(root)
	require.NoError(t, err)
	return ns
}

// TestForkToolchainCacheNamespace pins the namespace derivation: identical
// toolchain content yields the identical namespace (so one toolchain build
// keeps hitting its own cache entries across runs and machines), and ANY
// tool-binary difference yields a different namespace (so two different fork
// toolchain builds can never share cache entries — regardless of the version
// string they stamp, which is the constant-version collision that caused the
// 2026-07-20 cross-build poisoning).
func TestForkToolchainCacheNamespace(t *testing.T) {
	base := fingerprintGoroot(t, baseFakeGorootFiles())

	// Deterministic and canonical: 16 lowercase hex chars.
	assert.Regexp(t, regexp.MustCompile(`^[0-9a-f]{16}$`), base)
	assert.Equal(t, base, fingerprintGoroot(t, baseFakeGorootFiles()),
		"identical toolchain content must produce the identical namespace")

	// A one-byte compiler difference — with the SAME version string, the
	// exact incident shape — must change the namespace.
	changed := baseFakeGorootFiles()
	changed["pkg/tool/linux_amd64/compile"] = "compile binary content!"
	assert.NotEqual(t, base, fingerprintGoroot(t, changed),
		"two toolchains with different compilers must never share a namespace")

	// A changed bin/go (cmd/go's own hashing logic) must change it too.
	changed = baseFakeGorootFiles()
	changed["bin/go"] = "different go binary"
	assert.NotEqual(t, base, fingerprintGoroot(t, changed))

	// A changed VERSION alone must change it (release identity is content).
	changed = baseFakeGorootFiles()
	changed["VERSION"] = "go1.27cosmo"
	assert.NotEqual(t, base, fingerprintGoroot(t, changed))

	// An added tool must change it.
	changed = baseFakeGorootFiles()
	changed["pkg/tool/linux_amd64/cover"] = "cover binary content"
	assert.NotEqual(t, base, fingerprintGoroot(t, changed))
}

// TestForkToolchainCacheNamespaceFailsClosed: a GOROOT without tool binaries
// cannot be fingerprinted — that is an error, never a silent empty namespace
// (an un-namespaced fork build would reopen cross-toolchain poisoning).
func TestForkToolchainCacheNamespaceFailsClosed(t *testing.T) {
	// Missing pkg/tool entirely.
	root := filepath.Join(t.TempDir(), "goroot")
	writeFakeForkGoroot(t, root, map[string]string{"bin/go": "go"})
	_, err := forkToolchainCacheNamespace(root)
	assert.Error(t, err)

	// pkg/tool present but empty of files.
	root = filepath.Join(t.TempDir(), "goroot")
	writeFakeForkGoroot(t, root, map[string]string{"bin/go": "go"})
	require.NoError(t, os.MkdirAll(filepath.Join(root, "pkg", "tool", "linux_amd64"), 0755))
	_, err = forkToolchainCacheNamespace(root)
	assert.Error(t, err)

	// Nonexistent GOROOT.
	_, err = forkToolchainCacheNamespace(filepath.Join(t.TempDir(), "nope"))
	assert.Error(t, err)
}
