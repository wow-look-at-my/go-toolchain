//go:build !linux && !cosmo && !darwin

package cmd

// No /proc, so the guard cannot classify stdout and never fires here.
func inspectStdout() outputSink { return outputSink{kind: sinkVisible} }
