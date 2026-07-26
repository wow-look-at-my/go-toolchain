package logger

import (
	"sync"
	"sync/atomic"
)

// MaxRecordedWarnings bounds how many emitted warning messages are kept for
// the gate's end-of-run recap. The counter itself is never bounded; only the
// retained text is, so a pathological run cannot grow the slice without limit.
// Callers report the difference between WarnCount and len(EmittedWarnings) as
// the number of warnings not shown.
const MaxRecordedWarnings = 200

// warnCount tracks every Warn-level message actually emitted (i.e. after
// level filtering) by any Logger in this process. It backs the pipeline's
// warnings budget: a run that emits more than the allowed number of warnings
// fails at the end (see src/cmd's warnings gate). The counter is process-wide
// and deliberately counts only what the user actually saw — a Warn suppressed
// by --log-level error/silent is not counted. The cacheprog subprocess is a
// separate process, so its warnings never reach this counter.
var warnCount atomic.Int64

// warnMessages holds the text of the first MaxRecordedWarnings emitted
// warnings, in emission order, so the warnings gate can re-print exactly what
// it is failing on instead of just a number (a bare count invites guessing at
// which output was to blame — and the loud things in a build log are usually
// not the counted ones).
var (
	warnMu       sync.Mutex
	warnMessages []string
)

// recordWarn counts one emitted warning and retains its message text.
// Called by Warn/WarnFile after level filtering, with the caller's message
// already formatted. Callers hold their own Logger's mutex; this function
// takes only warnMu, so the lock order is fixed and cannot deadlock.
func recordWarn(msg string) {
	warnCount.Add(1)
	warnMu.Lock()
	defer warnMu.Unlock()
	if len(warnMessages) < MaxRecordedWarnings {
		warnMessages = append(warnMessages, msg)
	}
}

// WarnCount returns the number of Warn-level messages emitted so far in this
// process (across all Logger instances, after level filtering).
func WarnCount() int64 {
	return warnCount.Load()
}

// EmittedWarnings returns the retained warning messages in emission order
// (at most MaxRecordedWarnings of them). The result is a copy.
func EmittedWarnings() []string {
	warnMu.Lock()
	defer warnMu.Unlock()
	out := make([]string, len(warnMessages))
	copy(out, warnMessages)
	return out
}

// ResetWarnCount zeroes the process-wide warning counter and discards the
// retained warning messages. Intended for tests, which share one process and
// would otherwise observe each other's warnings.
func ResetWarnCount() {
	warnCount.Store(0)
	warnMu.Lock()
	defer warnMu.Unlock()
	warnMessages = nil
}
