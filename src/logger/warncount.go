package logger

import "sync"

// MaxRecordedWarnings bounds retained distinct warning text for the recap; the distinct count itself stays unbounded.
const MaxRecordedWarnings = 200

// Warning is one distinct emitted warning: the message text, and the number of
// times that exact text was emitted.
type Warning struct {
	Message string
	Count   int
}

// warnIndex maps a message to its slot, or -1 past the cap, so a repeat still
// folds. Never normalize the text. see docs/WARNINGS-GATE.md
var (
	warnMu       sync.Mutex
	warnDistinct int64
	warnTotal    int64
	warnIndex    map[string]int
	warnMessages []Warning
)

// recordWarn counts one emitted warning and retains the message text. A
// message already seen adds to its repeat count and leaves the distinct count
// alone. Callers hold their own Logger's mutex; this takes only warnMu, so the
// lock order is fixed and cannot deadlock.
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
// in this process. The warnings budget gates on this number.
func WarnCount() int64 {
	warnMu.Lock()
	defer warnMu.Unlock()
	return warnDistinct
}

// TotalWarnCount returns every Warn-level message emitted so far, repeats
// included, so a folded repeat stays visible in the recap.
func TotalWarnCount() int64 {
	warnMu.Lock()
	defer warnMu.Unlock()
	return warnTotal
}

// EmittedWarnings returns the retained distinct warnings (at most
// MaxRecordedWarnings), in first-emission order, each with its repeat count.
func EmittedWarnings() []Warning {
	warnMu.Lock()
	defer warnMu.Unlock()
	out := make([]Warning, len(warnMessages))
	copy(out, warnMessages)
	return out
}

// ResetWarnCount zeroes the process-wide counters, for tests sharing one process.
func ResetWarnCount() {
	warnMu.Lock()
	defer warnMu.Unlock()
	warnDistinct = 0
	warnTotal = 0
	warnIndex = nil
	warnMessages = nil
}
