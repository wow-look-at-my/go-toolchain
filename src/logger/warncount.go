package logger

import "sync"

// MaxRecordedWarnings bounds how many DISTINCT warning messages are kept for
// the gate's end-of-run recap. The distinct count itself is never bounded;
// only the retained text is, so a pathological run cannot grow the slice
// without limit. Callers report the difference between WarnCount and
// len(EmittedWarnings) as the number of warnings not shown.
const MaxRecordedWarnings = 200

// Warning is one distinct emitted warning: the message text, and the number of
// times that exact text was emitted.
type Warning struct {
	Message string
	Count   int
}

// The warnings budget counts DISTINCT messages. One root cause repeats once
// per file, per module or per retry. If each repeat counted, one problem
// spends the whole budget and every other warning in the run goes unreported.
// Two warnings are the same warning when the recorded text is identical. A
// message that names the file or the value it found stays distinct: WarnFile
// records the "<file>: " prefix, so the same text about two files counts
// twice. Deduplication governs the COUNT only. Every warning still prints, and
// the recap names each repeat count.
//
// warnDistinct backs the pipeline's warnings budget: a run that emits more
// than the allowed number of distinct warnings fails at the end (see src/cmd's
// warnings gate). warnTotal is every emission. The recap reports it, so a
// folded repeat stays visible. Both counters deliberately count only what the
// user actually saw -- a Warn suppressed by --log-level error/silent is not
// counted. The cacheprog subprocess is a separate process, so its warnings
// never reach these counters.
//
// warnIndex is the deduplication set. It maps a message to the position in
// warnMessages, or to -1 for a distinct message past MaxRecordedWarnings. It
// holds one entry for each distinct message, so its size follows the variety
// of the warning text, not the volume of the output.
//
// warnMessages holds the first MaxRecordedWarnings distinct warnings, in
// first-emission order, so the warnings gate can re-print exactly what it
// fails on instead of just a number (a bare count invites guessing at which
// output was to blame -- and the loud things in a build log are usually not
// the counted ones).
var (
	warnMu       sync.Mutex
	warnDistinct int64
	warnTotal    int64
	warnIndex    map[string]int
	warnMessages []Warning
)

// recordWarn counts one emitted warning and retains the message text. A
// message already seen adds to that warning's repeat count and leaves the
// distinct count alone. Warn/WarnFile call this after level filtering, with
// the caller's message already formatted. Callers hold their own Logger's
// mutex; this function takes only warnMu, so the lock order is fixed and
// cannot deadlock.
func recordWarn(msg string) {
	warnMu.Lock()
	defer warnMu.Unlock()
	warnTotal++
	if i, seen := warnIndex[msg]; seen {
		if i >= 0 {
			warnMessages[i].Count++
		}
		return
	}
	warnDistinct++
	i := -1
	if len(warnMessages) < MaxRecordedWarnings {
		warnMessages = append(warnMessages, Warning{Message: msg, Count: 1})
		i = len(warnMessages) - 1
	}
	if warnIndex == nil {
		warnIndex = make(map[string]int)
	}
	warnIndex[msg] = i
}

// WarnCount returns the number of DISTINCT Warn-level messages emitted so far
// in this process (across all Logger instances, after level filtering). The
// warnings budget gates on this number.
func WarnCount() int64 {
	warnMu.Lock()
	defer warnMu.Unlock()
	return warnDistinct
}

// TotalWarnCount returns every Warn-level message emitted so far, repeats
// included. The budget gates on WarnCount; the recap reports this number
// beside it, so a folded repeat stays visible.
func TotalWarnCount() int64 {
	warnMu.Lock()
	defer warnMu.Unlock()
	return warnTotal
}

// EmittedWarnings returns the retained distinct warnings in first-emission
// order (at most MaxRecordedWarnings of them), each with its repeat count.
// The result is a copy.
func EmittedWarnings() []Warning {
	warnMu.Lock()
	defer warnMu.Unlock()
	out := make([]Warning, len(warnMessages))
	copy(out, warnMessages)
	return out
}

// ResetWarnCount zeroes the process-wide warning counters and discards the
// retained warnings. Intended for tests, which share one process and would
// otherwise observe each other's warnings.
func ResetWarnCount() {
	warnMu.Lock()
	defer warnMu.Unlock()
	warnDistinct = 0
	warnTotal = 0
	warnIndex = nil
	warnMessages = nil
}
