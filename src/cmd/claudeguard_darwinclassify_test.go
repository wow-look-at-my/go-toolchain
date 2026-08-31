package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// okProbes answers every question, so a case can override just the probe it is
// about. Defaults are deliberately boring: a regular file at a non-capture
// path, no socket peer, not a terminal.
func okProbes(mode uint32) darwinFDProbes {
	return darwinFDProbes{
		fileType:      func() (uint32, bool) { return mode, true },
		socketPeer:    func() (int, bool, bool) { return 0, false, true },
		isTerminal:    func() (bool, bool) { return false, true },
		path:          func() (string, bool) { return "/tmp/out.log", true },
		peerName:      func(int) (string, bool) { return "", false },
		isAgentReader: func(string, int) bool { return false },
		isAgentPID:    func(int) bool { return false },
	}
}

func TestClassifyDarwinFD(t *testing.T) {
	// The behavior the whole design turns on: a probe this build cannot
	// make is NOT a negative answer. Answering "hidden" would refuse every
	// legitimate agent run on a Mac; answering "visible" would leave the guard
	// silently off. Both are worse than admitting blindness.
	t.Run("an unaskable probe reports blind, never a verdict", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			mutate func(*darwinFDProbes)
			mode   uint32
		}{
			{"file type", func(p *darwinFDProbes) {
				p.fileType = func() (uint32, bool) { return 0, false }
			}, sIFREG},
			{"socket peer", func(p *darwinFDProbes) {
				p.socketPeer = func() (int, bool, bool) { return 0, false, false }
			}, sIFSOCK},
			{"path", func(p *darwinFDProbes) {
				p.path = func() (string, bool) { return "", false }
			}, sIFREG},
		} {
			t.Run(tc.name, func(t *testing.T) {
				p := okProbes(tc.mode)
				tc.mutate(&p)
				_, ok := classifyDarwinFD(p)
				assert.False(t, ok, "an unaskable %s must report blind", tc.name)
			})
		}
	})

	// An unidentified FIFO still fails CLOSED -- `| cat` is indistinguishable
	// from grok-build's capture until the reader is named.
	t.Run("fifo fails closed", func(t *testing.T) {
		sink, ok := classifyDarwinFD(okProbes(sIFIFO))
		assert.True(t, ok)
		assert.Equal(t, sinkPipe, sink.kind)
	})

	t.Run("fifo", func(t *testing.T) {
		t.Run("the agent reading its own child is visible", func(t *testing.T) {
			p := okProbes(sIFIFO)
			p.fifoPeer = func() (int, bool, bool) { return 4242, true, true }
			p.peerName = func(pid int) (string, bool) {
				assert.Equal(t, 4242, pid)
				return "grok-build", true
			}
			p.isAgentReader = func(comm string, pid int) bool { return comm == "grok-build" }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkVisible, sink.kind)
		})

		t.Run("a filter is still a pipe, and named", func(t *testing.T) {
			p := okProbes(sIFIFO)
			p.fifoPeer = func() (int, bool, bool) { return 99, true, true }
			p.peerName = func(int) (string, bool) { return "cat", true }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkPipe, sink.kind)
			assert.Equal(t, "cat", sink.detail)
		})

		t.Run("a nameless peer the agent claims as its own is visible", func(t *testing.T) {
			p := okProbes(sIFIFO)
			p.fifoPeer = func() (int, bool, bool) { return 77, true, true }
			p.isAgentPID = func(pid int) bool { return pid == 77 }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkVisible, sink.kind)
		})
	})

	t.Run("socket", func(t *testing.T) {
		t.Run("the agent reading its own child is visible", func(t *testing.T) {
			p := okProbes(sIFSOCK)
			p.socketPeer = func() (int, bool, bool) { return 4242, true, true }
			p.peerName = func(pid int) (string, bool) {
				assert.Equal(t, 4242, pid)
				return "claude", true
			}
			p.isAgentReader = func(comm string, pid int) bool { return comm == "claude" }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkVisible, sink.kind, "refusing here breaks every agent run on a Mac")
		})

		t.Run("anyone else capturing is hidden, and named", func(t *testing.T) {
			p := okProbes(sIFSOCK)
			p.socketPeer = func() (int, bool, bool) { return 99, true, true }
			p.peerName = func(int) (string, bool) { return "tee", true }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkHidden, sink.kind)
			assert.Equal(t, "tee", sink.detail)
		})

		t.Run("an unresolvable peer is hidden, and says which pid", func(t *testing.T) {
			p := okProbes(sIFSOCK)
			p.socketPeer = func() (int, bool, bool) { return 77, true, true }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkHidden, sink.kind)
			assert.Equal(t, "pid 77", sink.detail)
		})

		// Naming the peer needs the ps tool, which a sandbox can refuse; the pid the
		// agent published is the identification that survives that.
		t.Run("a nameless peer the agent claims as its own is visible", func(t *testing.T) {
			p := okProbes(sIFSOCK)
			p.socketPeer = func() (int, bool, bool) { return 77, true, true }
			p.isAgentPID = func(pid int) bool { return pid == 77 }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkVisible, sink.kind, "an unrunnable ps must not refuse the agent's own reader")
		})

		// The pid fallback answers only where the name could not be read. A
		// resolved name that is not an agent is a decided answer already, and
		// re-deciding it on a pid an agent published for a DIFFERENT process
		// would wave a real capture through.
		t.Run("a named non-agent stays hidden whatever the pid claims", func(t *testing.T) {
			p := okProbes(sIFSOCK)
			p.socketPeer = func() (int, bool, bool) { return 77, true, true }
			p.peerName = func(int) (string, bool) { return "tee", true }
			p.isAgentPID = func(int) bool { return true }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkHidden, sink.kind)
			assert.Equal(t, "tee", sink.detail)
		})

		t.Run("no peer at all is hidden", func(t *testing.T) {
			sink, ok := classifyDarwinFD(okProbes(sIFSOCK))
			assert.True(t, ok)
			assert.Equal(t, sinkHidden, sink.kind)
		})
	})

	t.Run("char device", func(t *testing.T) {
		t.Run("a terminal is visible", func(t *testing.T) {
			p := okProbes(sIFCHR)
			p.isTerminal = func() (bool, bool) { return true, true }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkVisible, sink.kind)
		})

		t.Run("a pty a known wrapper allocated is not visible", func(t *testing.T) {
			old := ptyWrapperAncestorFn
			ptyWrapperAncestorFn = func() (string, bool) { return "script", true }
			t.Cleanup(func() { ptyWrapperAncestorFn = old })

			p := okProbes(sIFCHR)
			p.isTerminal = func() (bool, bool) { return true, true }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkHidden, sink.kind)
			assert.Equal(t, "script", sink.detail)
		})

		t.Run("dev null is discard, named", func(t *testing.T) {
			p := okProbes(sIFCHR)
			p.path = func() (string, bool) { return "/dev/null", true }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkDiscard, sink.kind)
			assert.Equal(t, "/dev/null", sink.detail)
		})

		// An APE on a darwin host cannot ask TCGETS, and blind on a char device
		// waves `> /dev/null` through. The device's own path decides instead.
		t.Run("an unaskable terminal probe falls back to the device path", func(t *testing.T) {
			p := okProbes(sIFCHR)
			p.isTerminal = func() (bool, bool) { return false, false }
			p.path = func() (string, bool) { return "/dev/null", true }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkDiscard, sink.kind)
			assert.Equal(t, "/dev/null", sink.detail)
		})

		t.Run("an unaskable terminal probe reads a tty path as visible", func(t *testing.T) {
			p := okProbes(sIFCHR)
			p.isTerminal = func() (bool, bool) { return false, false }
			p.path = func() (string, bool) { return "/dev/ttys003", true }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkVisible, sink.kind)
		})

		t.Run("an unaskable terminal probe with no path stays blind", func(t *testing.T) {
			p := okProbes(sIFCHR)
			p.isTerminal = func() (bool, bool) { return false, false }
			p.path = func() (string, bool) { return "", false }
			_, ok := classifyDarwinFD(p)
			assert.False(t, ok)
		})

		// The path only names the device in the message, so losing it must not
		// turn a decided classification into a blind classification.
		t.Run("an unaskable path still classifies", func(t *testing.T) {
			p := okProbes(sIFCHR)
			p.path = func() (string, bool) { return "", false }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkDiscard, sink.kind)
		})
	})

	t.Run("regular file", func(t *testing.T) {
		t.Run("a redirect is hidden, named", func(t *testing.T) {
			sink, ok := classifyDarwinFD(okProbes(sIFREG))
			assert.True(t, ok)
			assert.Equal(t, sinkFile, sink.kind)
			assert.Equal(t, "/tmp/out.log", sink.detail)
		})

		t.Run("the harness's own capture is visible", func(t *testing.T) {
			old := isCapturePathFn
			isCapturePathFn = func(path string) bool { return path == "/tmp/transcript" }
			t.Cleanup(func() { isCapturePathFn = old })

			p := okProbes(sIFREG)
			p.path = func() (string, bool) { return "/tmp/transcript", true }
			sink, ok := classifyDarwinFD(p)
			assert.True(t, ok)
			assert.Equal(t, sinkVisible, sink.kind)
		})
	})

	// An unrecognized type is real uncertainty about a descriptor, not a
	// missing capability: do not block on it.
	t.Run("an unknown type does not block", func(t *testing.T) {
		sink, ok := classifyDarwinFD(okProbes(0x3000))
		assert.True(t, ok)
		assert.Equal(t, sinkVisible, sink.kind)
	})

	// The mode carries permission bits too; only the type bits decide.
	t.Run("permission bits are masked off", func(t *testing.T) {
		sink, ok := classifyDarwinFD(okProbes(sIFIFO | 0o644))
		assert.True(t, ok)
		assert.Equal(t, sinkPipe, sink.kind)
	})
}
