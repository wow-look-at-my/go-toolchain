//go:build !linux

package cmd

// claudeProcessAncestor is a no-op on non-Linux platforms (no /proc). Detection
// there falls back to the CLAUDECODE environment marker in runningUnderClaude.
func claudeProcessAncestor() bool { return false }

// inspectStdout cannot classify the stdout descriptor without /proc, so the
// output guard never fires on non-Linux platforms.
func inspectStdout() outputSink { return outputSink{kind: sinkVisible} }
