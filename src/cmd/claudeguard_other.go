//go:build !linux && !cosmo

package cmd

// agentProcessAncestor is a no-op on platforms without /proc (native darwin
// and windows builds). Detection there falls back to the agents' environment
// markers in runningUnderAgent.
func agentProcessAncestor() (string, bool) { return "", false }

// inspectStdout cannot classify the stdout descriptor without /proc, so the
// output guard never fires on non-linux, non-cosmo platforms. (The released
// "linux" binaries are GOOS=cosmo APE copies and use the real classifier in
// claudeguard_proc.go — this stub must never shadow it there.)
func inspectStdout() outputSink { return outputSink{kind: sinkVisible} }
