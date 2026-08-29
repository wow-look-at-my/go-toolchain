// A pty slave passes isatty regardless of who allocated it. The script tool (and
// asciinema, ttyrec, unbuffer, expect) forkpty()s a fresh pty around a
// child so the child's isatty checks pass, then copies the pty's output to
// a file. `script -qec "go-toolchain" out.log` turns a would-be captured
// stdout into an apparent terminal.
//
// isatty cannot see the wrapper. Ancestry can: the char-device branch in
// claudeguard_proc.go and claudeguard_darwinclassify.go also checks for a
// known pty-wrapping tool in the parent chain. Advisory, like agent
// detection itself: a renamed or unlisted wrapper is a known gap.

package cmd

import (
	"os"

	"github.com/wow-look-at-my/go-containers/set"
	agent "github.com/wow-look-at-my/is-this-an-agent"
)

// ptyWrapperProcs give a child a fresh pty, so isatty passes. Exact comm match. Depth: docs/AGENT-OUTPUT-GUARD.md
var ptyWrapperProcs = set.Of(
	"script",
	"scriptreplay",
	"ttyrec",
	"ttyplay",
	"asciinema",
	"unbuffer",
	"expect",
)

// ptyWrapperMaxHops bounds the ancestry walk against a pid-reuse cycle.
const ptyWrapperMaxHops = 64

// Indirection seams so tests can drive the walk without a real process tree.
var (
	ptyWrapperParentPIDFn = os.Getppid
	ptyWrapperCommPPIDFn  = agent.CommPPID
)

// ptyWrapperAncestorFn is the seam the classifiers call.
var ptyWrapperAncestorFn = ptyWrapperAncestor

// ptyWrapperAncestor walks the parent chain for a known pty-wrapping tool.
// An unresolvable ancestor ends the walk as "not found", never a guess.
func ptyWrapperAncestor() (string, bool) {
	pid := ptyWrapperParentPIDFn()
	for hops := 0; pid > 1 && hops < ptyWrapperMaxHops; hops++ {
		comm, ppid, ok := ptyWrapperCommPPIDFn(pid)
		if !ok {
			return "", false
		}
		if ptyWrapperProcs.Contains(comm) {
			return comm, true
		}
		pid = ppid
	}
	return "", false
}
