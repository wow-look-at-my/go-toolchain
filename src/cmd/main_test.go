package cmd

import (
	"os"
	"testing"
)

// TestMain keeps every mock build off buildhost. A test of the release itself
// calls ensureReleaseEnv directly.
func TestMain(m *testing.M) {
	ensureReleaseEnvFunc = func(string) error { return nil }
	os.Unsetenv(gosmoReleaseEnv)
	os.Unsetenv(gosmoReleaseRevisionEnv)
	os.Exit(m.Run())
}
