//go:build linux || cosmo

package cmd

import (
	"bytes"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
)

// captureGuardOut redirects the guard's stderr writer and resets the
// once-per-run latch, so each case observes its own output.
func captureGuardOut(t *testing.T, f func()) string {
	t.Helper()
	var buf bytes.Buffer
	old := agentGuardOut
	agentGuardOut = &buf
	guardInoperativeOnce = sync.Once{}
	t.Cleanup(func() {
		agentGuardOut = old
		guardInoperativeOnce = sync.Once{}
	})
	f()
	return buf.String()
}

// A blind classifier must ANNOUNCE that it is blind. Answering sinkVisible is
// correct — a guard that cannot see must not refuse a legitimate run — but
// that answer means "the guard is not running", and a guard that silently is
// not running is worse than one that is loudly absent.
//
// Driven through blindClassifierSink rather than unclassifiableSink so the
// banner's CONTENT is asserted on every platform's CI. Reaching it through the
// host dispatch would make this a no-op on the linux runners that are the only
// place this suite runs, which is how the banner could regress unseen.
func TestBlindClassifierSinkAnnouncesItself(t *testing.T) {
	var got outputSink
	out := captureGuardOut(t, func() { got = blindClassifierSink("darwin") })

	assert.Equal(t, sinkVisible, got.kind, "a classifier that cannot see must never refuse")
	assert.Contains(t, out, "INOPERATIVE")
	assert.Contains(t, out, "darwin")
}

// On a linux host an unreadable descriptor is one odd fd, not a blind guard:
// the /proc classifier works there, so this must stay silent or it cries wolf
// on every run.
func TestUnclassifiableSinkIsSilentOnALinuxHost(t *testing.T) {
	if hostos.GOOS() != "linux" {
		t.Skip("this asserts the linux-host branch")
	}
	var got outputSink
	out := captureGuardOut(t, func() { got = unclassifiableSink() })
	assert.Equal(t, sinkVisible, got.kind)
	assert.Empty(t, out)
}

// The announcement is once per run, not once per descriptor: the guard
// inspects stdout on every invocation and a repeated banner would train the
// reader to skip it.
func TestBlindGuardAnnouncementIsOncePerRun(t *testing.T) {
	out := captureGuardOut(t, func() {
		blindClassifierSink("darwin")
		blindClassifierSink("darwin")
		blindClassifierSink("darwin")
	})
	assert.Equal(t, 1, bytes.Count([]byte(out), []byte("INOPERATIVE")))
}

// The banner must never reach stdout. The guard exists for runs whose stdout
// is captured, so stdout is the one channel that cannot carry its own warning.
func TestBlindGuardAnnouncementNeverTouchesStdout(t *testing.T) {
	var buf bytes.Buffer
	old := agentGuardOut
	agentGuardOut = &buf
	guardInoperativeOnce = sync.Once{}
	t.Cleanup(func() {
		agentGuardOut = old
		guardInoperativeOnce = sync.Once{}
	})

	stdout := captureStdout(func() { unclassifiableSink() })
	assert.Empty(t, stdout)
	require.NotNil(t, io.Writer(&buf))
}
