package logger

import "sync/atomic"

// warnCount tracks every Warn-level message actually emitted (i.e. after
// level filtering) by any Logger in this process. It backs the pipeline's
// warnings budget: a run that emits more than the allowed number of warnings
// fails at the end (see src/cmd's warnings gate). The counter is process-wide
// and deliberately counts only what the user actually saw — a Warn suppressed
// by --log-level error/silent is not counted. The cacheprog subprocess is a
// separate process, so its warnings never reach this counter.
var warnCount atomic.Int64

// WarnCount returns the number of Warn-level messages emitted so far in this
// process (across all Logger instances, after level filtering).
func WarnCount() int64 {
	return warnCount.Load()
}

// ResetWarnCount zeroes the process-wide warning counter. Intended for tests,
// which share one process and would otherwise observe each other's warnings.
func ResetWarnCount() {
	warnCount.Store(0)
}
