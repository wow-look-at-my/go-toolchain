package cache

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// socketRoot stays short: os.TempDir() reads TMPDIR, which a test points
// at its own long t.TempDir().
func socketRoot() string {
	if fi, err := os.Stat("/tmp"); err == nil && fi.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
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

// TestSocketPathIgnoresTMPDIR: a test that moves TMPDIR to its own t.TempDir()
// is what overflowed sun_path, and bind answers "invalid argument" rather than
// naming a length. The bind is the proof; the path check is what fails on a
// host whose own paths are short enough to hide it.
func TestSocketPathIgnoresTMPDIR(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	got := testSocketPath(t, "probe.sock")
	require.NotContains(t, got, tmp, "the socket must not inherit a test's TMPDIR")

	l, err := net.Listen("unix", got)
	require.NoError(t, err)
	require.NoError(t, l.Close())
}
