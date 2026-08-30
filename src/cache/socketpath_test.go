package cache

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// The temp dir as it stood before any test moved it: init beats every t.Setenv.
var startupTempDir = os.TempDir()

// socketRoot stays short: os.TempDir() reads TMPDIR on a unix host and TMP or
// TEMP on NT, and a test points those at its own long t.TempDir().
func socketRoot() string {
	if fi, err := os.Stat("/tmp"); err == nil && fi.IsDir() {
		return "/tmp"
	}
	return startupTempDir
}

// testSocketPath names a socket short enough to bind: sun_path is far smaller
// than a path, and t.TempDir() spends it on darwin's temp dir plus the test name.
func testSocketPath(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.MkdirTemp(socketRoot(), "gtc")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, name)
}

// A test that moves the temp dir to its own t.TempDir() overflows sun_path,
// and bind answers "invalid argument" rather than naming a length. The bind is
// the proof; the path check is what fails where paths are short enough to hide
// it. setTempDir moves every name a host reads, so this covers NT too.
func TestSocketPathIgnoresTMPDIR(t *testing.T) {
	tmp := t.TempDir()
	setTempDir(t, tmp)

	got := testSocketPath(t, "probe.sock")
	require.NotContains(t, got, tmp, "the socket must not inherit a test's TMPDIR")

	l, err := net.Listen("unix", got)
	require.NoError(t, err)
	require.NoError(t, l.Close())
}
