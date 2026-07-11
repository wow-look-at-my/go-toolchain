package cache

import (
	"os"
	"testing"
)

// TestMain isolates this package's tests from an ENCLOSING go-toolchain build.
// When the tests run under go-toolchain itself, the parent process exports
// GOCACHE_STATS_SOCK for its own cacheprog and the test binary inherits it, so
// every NewServer/NewDaemon constructed by a test would dial the OUTER build's
// stats listener — polluting its cache-line accounting with fake test events
// and, when the outer binary predates the accept-ack handshake (and so never
// writes an ack), stalling each constructor for the full 5s ack deadline —
// enough stalls to blow the test binary's overall timeout. Tests that exercise
// stats delivery own their listener and set the variable via t.Setenv.
func TestMain(m *testing.M) {
	os.Unsetenv("GOCACHE_STATS_SOCK")
	os.Exit(m.Run())
}
