//go:build darwin || cosmo

// The classifier for a darwin HOST, shared by the two builds that can run on
// one: a native GOOS=darwin binary, and a GOOS=cosmo fat APE executing on a
// Mac. One algorithm, because two copies would drift and the copy nobody runs
// locally is the one that would rot.
//
// darwin has no /proc, so this cannot reuse claudeguard_proc.go. What varies
// per build is only HOW the four probes are made
// (claudeguard_fdprobe_{darwin,cosmo}.go); what is decided from them lives in
// claudeguard_darwinclassify.go, unconstrained so it is tested everywhere.

package cmd

import agent "github.com/wow-look-at-my/is-this-an-agent"

// inspectFDDarwinHost classifies fd on a darwin host. ok is false when a probe
// this build cannot make was needed -- not "the answer was no", but "the
// question could not be asked" -- and the caller must then treat the
// classifier as blind rather than acting on a partial answer.
func inspectFDDarwinHost(fd uintptr) (outputSink, bool) {
	return classifyDarwinFD(darwinFDProbes{
		fileType:   func() (uint32, bool) { return fdFileTypeOnDarwinHost(fd) },
		socketPeer: func() (int, bool, bool) { return socketPeerOnDarwinHost(fd) },
		isTerminal: func() (bool, bool) { return isTerminalOnDarwinHost(fd) },
		path:       func() (string, bool) { return fdPathOnDarwinHost(fd) },
		peerName: func(pid int) (string, bool) {
			comm, _, ok := agent.CommPPID(pid)
			return comm, ok
		},
		isAgentReader: agent.IsPipeReader,
	})
}
