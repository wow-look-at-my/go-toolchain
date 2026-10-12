package cmd

import (
	"os"
	"testing"
)

// TestMain keeps every mock build off buildhost. A test of the release itself
// calls ensureReleaseEnv directly.
func TestMain(m *testing.M) {
	ensureReleaseEnvFunc = func() error { return nil }
	os.Exit(m.Run())
}
