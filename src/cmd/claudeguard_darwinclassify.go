package cmd

import (
	"fmt"

	agent "github.com/wow-look-at-my/is-this-an-agent"
)

// isCapturePathFn: the seam for the harness's transcript-capture check, so tests can drive it without a real agent.
var isCapturePathFn = agent.IsCapturePath

// Decision table: build-constraint-free, so CI tests it everywhere.
// File-type bits are spelled locally; stdlib omits S_IF* on some GOOS.
const (
	sIFMT   = 0xF000
	sIFIFO  = 0x1000
	sIFCHR  = 0x2000
	sIFREG  = 0x8000
	sIFSOCK = 0xC000
)

// darwinFDProbes are the questions the classifier asks about a descriptor.
// Each probe reports supported=false when this build cannot ask it at all --
// which is different from a negative answer, and must not be read as such.
type darwinFDProbes struct {
	// fileType returns the descriptor's S_IFMT bits.
	fileType func() (mode uint32, supported bool)
	// socketPeer returns the pid the kernel recorded on the far end.
	socketPeer func() (pid int, identified, supported bool)
	// fifoPeer: pid of a FIFO's other end. identified=false fails closed -- a nameless reader is `| cat`.
	fifoPeer func() (pid int, identified, supported bool)
	// isTerminal reports whether a character device is a real terminal.
	isTerminal func() (terminal, supported bool)
	// path recovers a descriptor's path (darwin's F_GETPATH).
	path func() (string, bool)
	// peerName resolves a pid to its command name.
	peerName func(pid int) (comm string, ok bool)
	// isAgentReader: is that process the agent reading its own output, not something capturing it.
	isAgentReader func(comm string, pid int) bool
	// isAgentPID: did an agent name this pid as its own via env, needing no process lookup.
	isAgentPID func(pid int) bool
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
		// grok-build's stdout is a FIFO, not a socketpair. An unidentified
		// reader is indistinguishable from `| cat`, so it fails closed too.
		if p.fifoPeer == nil {
			return outputSink{kind: sinkPipe}, true
		}
		pid, identified, supported := p.fifoPeer()
		if !supported || !identified {
			return outputSink{kind: sinkPipe}, true
		}
		return peerSink(p, pid, sinkPipe), true

	case sIFSOCK:
		// Agent tool plumbing is a socketpair; the kernel fixes the peer at connect time, resolving after the parent's copy closes.
		pid, identified, supported := p.socketPeer()
		if !supported {
			return outputSink{}, false
		}
		if !identified {
			return outputSink{kind: sinkHidden}, true
		}
		return peerSink(p, pid, sinkHidden), true

	case sIFCHR:
		terminal, supported := p.isTerminal()
		if !supported {
			return outputSink{}, false
		}
		if terminal {
			// Same gap claudeguard_ptywrap.go closes on linux/cosmo.
			if wrapper, ok := ptyWrapperAncestorFn(); ok {
				return outputSink{kind: sinkHidden, detail: wrapper}, true
			}
			return outputSink{kind: sinkVisible}, true
		}
		path, _ := p.path() // best effort: only names the device in the message
		return outputSink{kind: sinkDiscard, detail: path}, true

	case sIFREG:
		// The path separates the harness's transcript capture from an ordinary redirect; without it there's nothing to decide.
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

// peerSink classifies a descriptor whose far-end pid is known: the agent
// reading its own child is visible, anything else is `notAgent` (sinkPipe
// for a FIFO, sinkHidden for a socket) and names the reader. A nameless
// peer still answers when an agent published that pid as its own -- the
// kernel fixed the peer and the agent named it, which `| cat` cannot
// arrange.
func peerSink(p darwinFDProbes, pid int, notAgent sinkKind) outputSink {
	if comm, ok := p.peerName(pid); ok {
		if p.isAgentReader(comm, pid) {
			return outputSink{kind: sinkVisible}
		}
		return outputSink{kind: notAgent, detail: comm}
	}
	if p.isAgentPID != nil && p.isAgentPID(pid) {
		return outputSink{kind: sinkVisible}
	}
	return outputSink{kind: notAgent, detail: fmt.Sprintf("pid %d", pid)}
}
