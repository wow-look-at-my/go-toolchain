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
