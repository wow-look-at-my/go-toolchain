package cache

import (
	"os"
	"testing"
)

// TestMain clears an inherited GOCACHE_STATS_SOCK; stats tests set their own via t.Setenv.
func TestMain(m *testing.M) {
	os.Unsetenv("GOCACHE_STATS_SOCK")
	os.Exit(m.Run())
}

// setTempDir points os.TempDir() at dir. Windows reads TMP and TEMP,
// other hosts TMPDIR, so set them all.
func setTempDir(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, dir)
	}
}
