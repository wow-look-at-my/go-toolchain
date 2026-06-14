//go:build !linux

package cmd

// claudeProcessAncestor is a no-op on non-Linux platforms (no /proc). Detection
// there falls back to the CLAUDECODE environment marker in runningUnderClaude.
func claudeProcessAncestor() bool { return false }

// stdoutPipePeerName cannot identify a pipe's consumer without /proc, so the
// pipe-filter guard never fires on non-Linux platforms.
func stdoutPipePeerName() (string, bool) { return "", false }
