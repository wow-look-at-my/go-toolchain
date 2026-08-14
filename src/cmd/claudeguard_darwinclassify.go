package cmd

import (
	"fmt"

	agent "github.com/wow-look-at-my/is-this-an-agent"
)

// isCapturePathFn is the seam for "is this the harness's own transcript
// capture", so the decision table can be driven in tests without a real agent.
var isCapturePathFn = agent.IsCapturePath

// The darwin classifier's decision table, kept free of build constraints so it
// is TESTED on every platform's CI rather than only on the one host that can
// run it. The syscalls that answer these questions are per-build
// (claudeguard_fdprobe_{darwin,cosmo}.go); what is decided from the answers is
// not, and that is the part with branches worth pinning.
//
// POSIX file-type bits, spelled locally: the stdlib syscall package does not
// export S_IF* on every GOOS this file compiles for, and the values are
// identical on linux and darwin.
const (
	sIFMT   = 0xF000
	sIFIFO  = 0x1000
	sIFCHR  = 0x2000
	sIFREG  = 0x8000
	sIFSOCK = 0xC000
)

// darwinFDProbes are the questions the classifier asks about one descriptor.
// Each probe reports supported=false when this build cannot ask it at all --
// which is different from a negative answer, and must not be treated as one.
type darwinFDProbes struct {
	// fileType returns the descriptor's S_IFMT bits.
	fileType func() (mode uint32, supported bool)
	// socketPeer returns the pid the kernel recorded on the far end.
	socketPeer func() (pid int, identified, supported bool)
	// isTerminal reports whether a character device is a real terminal.
	isTerminal func() (terminal, supported bool)
	// path recovers a descriptor's path (darwin's F_GETPATH).
	path func() (string, bool)
	// peerName resolves a pid to its command name.
	peerName func(pid int) (comm string, ok bool)
	// isAgentReader reports whether that process is the agent reading our
	// output, rather than something capturing it.
	isAgentReader func(comm string, pid int) bool
}

// classifyDarwinFD decides where a descriptor's output is going on a darwin
// host. ok is false when a probe the decision needed is unsupported here: the
// caller must then treat the classifier as blind and say so, never act on a
// partial answer. Guessing in either direction is a real cost -- refuse and
// every legitimate agent run on a Mac breaks; allow and the guard is off.
func classifyDarwinFD(p darwinFDProbes) (outputSink, bool) {
	mode, supported := p.fileType()
	if !supported {
		return outputSink{}, false
	}

	switch mode & sIFMT {
	case sIFIFO:
		// A FIFO's reader cannot be identified on darwin without libproc, so
		// an agent piping its own child's stdout back to itself is
		// indistinguishable from `| grep`. Fails CLOSED.
		return outputSink{kind: sinkPipe}, true

	case sIFSOCK:
		// What a coding agent's tool-execution plumbing actually is: a
		// socketpair for a spawned child's stdio. The kernel fixes the peer at
		// connection time, so this still resolves after the parent closes its
		// copy of the child's descriptor.
		pid, identified, supported := p.socketPeer()
		if !supported {
			return outputSink{}, false
		}
		if !identified {
			return outputSink{kind: sinkHidden}, true
		}
		if comm, ok := p.peerName(pid); ok {
			if p.isAgentReader(comm, pid) {
				return outputSink{kind: sinkVisible}, true
			}
			return outputSink{kind: sinkHidden, detail: comm}, true
		}
		return outputSink{kind: sinkHidden, detail: fmt.Sprintf("pid %d", pid)}, true

	case sIFCHR:
		terminal, supported := p.isTerminal()
		if !supported {
			return outputSink{}, false
		}
		if terminal {
			return outputSink{kind: sinkVisible}, true
		}
		path, _ := p.path() // best effort: only names the device in the message
		return outputSink{kind: sinkDiscard, detail: path}, true

	case sIFREG:
		// The path is what separates the harness's own transcript capture
		// (visible) from an ordinary `> out.log` (hidden), so without it there
		// is nothing to decide on.
		path, supported := p.path()
		if !supported {
			return outputSink{}, false
		}
		if path != "" && isCapturePathFn(path) {
			return outputSink{kind: sinkVisible}, true
		}
		return outputSink{kind: sinkFile, detail: path}, true
	}

	return outputSink{kind: sinkVisible}, true // unknown disposition -- don't block
}
