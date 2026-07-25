package logger

import (
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

// TestResetWarnCount verifies the reset helper zeroes the counter.
func TestResetWarnCount(t *testing.T) {
	ResetWarnCount()

	l, _, _ := captureLogger(LevelInfo, false)
	l.Warn("one")
	l.Warn("two")
	assert.Equal(t, int64(2), WarnCount())

	ResetWarnCount()
	assert.Equal(t, int64(0), WarnCount())
}

// TestWarnInfraIsPrintedButNotCounted pins the split between the two warning
// kinds. An infrastructure warning must be just as visible as a normal one --
// same "WARNING:" line locally, same ::warning annotation in GitHub Actions --
// while staying out of the warnings budget, so a slow cache server or a flaky
// network can never fail a build that has nothing wrong with it.
func TestWarnInfraIsPrintedButNotCounted(t *testing.T) {
	ResetWarnCount()

	l, _, errBuf := captureLogger(LevelInfo, false)
	l.WarnInfra("cache server unreachable")
	assert.Contains(t, errBuf.String(), "WARNING: cache server unreachable")
	assert.Equal(t, int64(0), WarnCount(), "infrastructure warnings must not consume the budget")

	gha, out, _ := captureLogger(LevelInfo, true)
	gha.WarnInfra("annotated infra warning")
	assert.Contains(t, out.String(), "::warning ::annotated infra warning")
	assert.Equal(t, int64(0), WarnCount())

	// Budgeted warnings still count, alongside the uncounted ones.
	l.Warn("your code has a problem")
	assert.Equal(t, int64(1), WarnCount())
}

// TestWarnInfraRespectsLevelFilter verifies WarnInfra honors the level filter
// exactly like Warn.
func TestWarnInfraRespectsLevelFilter(t *testing.T) {
	ResetWarnCount()

	l, out, errBuf := captureLogger(LevelError, false)
	l.WarnInfra("suppressed")
	assert.Equal(t, 0, out.Len())
	assert.Equal(t, 0, errBuf.Len())
	assert.Equal(t, int64(0), WarnCount())
}
