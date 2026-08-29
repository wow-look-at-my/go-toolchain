package logger

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWarnCountCountsEmittedWarnings verifies that every Warn and WarnFile
// message that is actually emitted increments the process-wide counter, in
// both plain and GHA annotation modes.
func TestWarnCountCountsEmittedWarnings(t *testing.T) {
	ResetWarnCount()

	l, _, errBuf := captureLogger(LevelInfo, false)
	l.Warn("plain warning")
	l.WarnFile("main.go", "file warning")
	assert.Contains(t, errBuf.String(), "plain warning")
	assert.Equal(t, int64(2), WarnCount())

	gha, out, _ := captureLogger(LevelInfo, true)
	gha.Warn("annotated warning")
	assert.Contains(t, out.String(), "::warning ::annotated warning")
	assert.Equal(t, int64(3), WarnCount())
}

// TestWarnCountIgnoresFilteredWarnings verifies that a Warn suppressed by the
// level filter is not counted — the counter tracks what the user actually saw.
func TestWarnCountIgnoresFilteredWarnings(t *testing.T) {
	ResetWarnCount()

	l, out, errBuf := captureLogger(LevelError, false)
	l.Warn("suppressed")
	l.WarnFile("main.go", "also suppressed")
	assert.Equal(t, 0, out.Len())
	assert.Equal(t, 0, errBuf.Len())
	assert.Equal(t, int64(0), WarnCount())
}

// TestWarnCountIgnoresOtherLevels verifies that Debug/Info/Error/Output
// emissions never move the warning counter.
func TestWarnCountIgnoresOtherLevels(t *testing.T) {
	ResetWarnCount()

	l, _, _ := captureLogger(LevelDebug, false)
	l.Debug("debug")
	l.Info("info")
	l.Error("error")
	l.Output("output")
	assert.Equal(t, int64(0), WarnCount())
}

// TestResetWarnCount verifies the reset helper zeroes both counters and the
// deduplication set, so a repeat after a reset counts again.
func TestResetWarnCount(t *testing.T) {
	ResetWarnCount()

	l, _, _ := captureLogger(LevelInfo, false)
	l.Warn("one")
	l.Warn("two")
	l.Warn("two")
	assert.Equal(t, int64(2), WarnCount())
	assert.Equal(t, int64(3), TotalWarnCount())

	ResetWarnCount()
	assert.Equal(t, int64(0), WarnCount())
	assert.Equal(t, int64(0), TotalWarnCount())

	l.Warn("two")
	assert.Equal(t, int64(1), WarnCount())
}

// TestEmittedWarningsRetainsMessages verifies that the text of every emitted
// warning is retained in emission order, with WarnFile keeping its file
// prefix, so the warnings gate can re-print exactly what it failed on.
func TestEmittedWarningsRetainsMessages(t *testing.T) {
	ResetWarnCount()
	defer ResetWarnCount()

	l, _, _ := captureLogger(LevelInfo, false)
	l.Warn("first %d", 1)
	l.WarnFile("main.go", "second")
	l.WithSubsystem("cache").Warn("third")

	want := []Warning{
		{Message: "first 1", Count: 1},
		{Message: "main.go: second", Count: 1},
		{Message: "cache: third", Count: 1},
	}
	assert.Equal(t, want, EmittedWarnings())
}

// TestWarnCountFoldsRepeats verifies the budget counts DISTINCT messages: a
// root cause that repeats per file or per retry must not spend the whole
// budget. The repeats are still emitted, still totalled, and carried on the
// retained warning so the recap can name them.
func TestWarnCountFoldsRepeats(t *testing.T) {
	ResetWarnCount()
	defer ResetWarnCount()

	l, _, errBuf := captureLogger(LevelInfo, false)
	l.Warn("cache: index fetch failed")
	l.Warn("cache: index fetch failed")
	l.Warn("cache: index fetch failed")
	l.Warn("a different problem")

	assert.Equal(t, int64(2), WarnCount())
	assert.Equal(t, int64(4), TotalWarnCount())
	want := []Warning{
		{Message: "cache: index fetch failed", Count: 3},
		{Message: "a different problem", Count: 1},
	}
	assert.Equal(t, want, EmittedWarnings())
	// Folding governs the count only: every repeat still reached the user.
	assert.Equal(t, 3, strings.Count(errBuf.String(), "cache: index fetch failed"))
}

// TestWarnFileFoldsPerFile verifies that the recorded "<file>: " prefix keeps
// the same message about separate files distinct, and folds the same message
// about a single file. A per-file warning names a per-file problem.
func TestWarnFileFoldsPerFile(t *testing.T) {
	ResetWarnCount()
	defer ResetWarnCount()

	l, _, _ := captureLogger(LevelInfo, false)
	l.WarnFile("a.go", "line too long")
	l.WarnFile("a.go", "line too long")
	l.WarnFile("b.go", "line too long")

	assert.Equal(t, int64(2), WarnCount())
	assert.Equal(t, int64(3), TotalWarnCount())
	want := []Warning{
		{Message: "a.go: line too long", Count: 2},
		{Message: "b.go: line too long", Count: 1},
	}
	assert.Equal(t, want, EmittedWarnings())
}

// TestWarnCountFoldsRepeatsPastRetention verifies that a repeat of a message
// too late to be retained still folds. The deduplication set outlives the
// retained text, so the budget cannot be inflated by repeating a warning that
// arrived past MaxRecordedWarnings.
func TestWarnCountFoldsRepeatsPastRetention(t *testing.T) {
	ResetWarnCount()
	defer ResetWarnCount()

	l, _, _ := captureLogger(LevelInfo, false)
	for i := range MaxRecordedWarnings + 1 {
		l.Warn("warning %d", i)
	}
	l.Warn("warning %d", MaxRecordedWarnings) // the unretained message, again

	assert.Equal(t, int64(MaxRecordedWarnings+1), WarnCount())
	assert.Equal(t, int64(MaxRecordedWarnings+2), TotalWarnCount())
	assert.Len(t, EmittedWarnings(), MaxRecordedWarnings)
}

// TestEmittedWarningsExcludesFiltered verifies that a warning suppressed by
// the log level is neither counted nor retained — the recap shows only what
// the user actually saw.
func TestEmittedWarningsExcludesFiltered(t *testing.T) {
	ResetWarnCount()
	defer ResetWarnCount()

	silenced, _, _ := captureLogger(LevelError, false)
	silenced.Warn("suppressed")
	silenced.WarnFile("main.go", "suppressed too")

	assert.Empty(t, EmittedWarnings())
	assert.Equal(t, int64(0), WarnCount())
}

// TestEmittedWarningsCapped verifies that retention is bounded while the
// counter keeps counting, so the gate can report how many it is not showing
// instead of silently truncating.
func TestEmittedWarningsCapped(t *testing.T) {
	ResetWarnCount()
	defer ResetWarnCount()

	l, _, _ := captureLogger(LevelInfo, false)
	for i := 0; i < MaxRecordedWarnings+5; i++ {
		l.Warn("warning %d", i)
	}

	assert.Equal(t, int64(MaxRecordedWarnings+5), WarnCount())
	assert.Len(t, EmittedWarnings(), MaxRecordedWarnings)
	assert.Equal(t, "warning 0", EmittedWarnings()[0].Message)
}

// TestResetWarnCountClearsMessages verifies the reset helper discards the
// retained messages along with the counter.
func TestResetWarnCountClearsMessages(t *testing.T) {
	ResetWarnCount()

	l, _, _ := captureLogger(LevelInfo, false)
	l.Warn("one")
	assert.Len(t, EmittedWarnings(), 1)

	ResetWarnCount()
	assert.Empty(t, EmittedWarnings())
}
