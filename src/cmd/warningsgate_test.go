package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// warnGateLogger returns a logger that emits into discarded buffers, so tests can drive
// the process-wide warning counter without printing to the test output.
func warnGateLogger(level logger.Level) *logger.Logger {
	return logger.New(logger.Options{
		Level:  level,
		Stdout: &strings.Builder{},
		Stderr: &strings.Builder{},
	})
}

// TestWarningsGateAtThreshold verifies that exactly maxWarnings warnings do
// NOT fail the build — the gate fires only when the threshold is exceeded.
func TestWarningsGateAtThreshold(t *testing.T) {
	t.Serial()
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()

	l := warnGateLogger(logger.LevelInfo)
	for i := 0; i < maxWarnings; i++ {
		l.Warn("warning %d", i+1)
	}
	require.Equal(t, int64(maxWarnings), logger.WarnCount())

	assert.NoError(t, checkWarningsGate())
}

// TestWarningsGateOverThreshold verifies that a warning past the budget
// fails the build with a message naming both
// the count and the threshold.
func TestWarningsGateOverThreshold(t *testing.T) {
	t.Serial()
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()

	l := warnGateLogger(logger.LevelInfo)
	for i := 0; i < maxWarnings+1; i++ {
		l.Warn("warning %d", i+1)
	}
	require.Equal(t, int64(maxWarnings+1), logger.WarnCount())

	err := checkWarningsGate()
	require.Error(t, err)
	assert.Equal(t, "build failed: 16 distinct warnings emitted (threshold: 15)", err.Error())
}

// TestWarningsGateFoldsRepeats verifies that a warning repeated far past the
// budget does not fail the build. A root cause repeats per file, per module
// or per retry; counting each repeat spends the budget on a lone problem and
// hides every other warning in the run.
func TestWarningsGateFoldsRepeats(t *testing.T) {
	t.Serial()
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()

	l := warnGateLogger(logger.LevelInfo)
	for range maxWarnings * 10 {
		l.Warn("cache: web index fetch failed")
	}
	require.Equal(t, int64(1), logger.WarnCount())
	require.Equal(t, int64(maxWarnings*10), logger.TotalWarnCount())

	assert.NoError(t, checkWarningsGate())
}

// TestWarningsGateRecapNamesRepeatCounts verifies the recap says how many
// times a folded warning was emitted, and reports the total beside the
// distinct count. Deduplication must not hide volume from the reader.
func TestWarningsGateRecapNamesRepeatCounts(t *testing.T) {
	t.Serial()
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()
	t.Setenv("GITHUB_ACTIONS", "false")

	out, errBuf := &strings.Builder{}, &strings.Builder{}
	logger.Init(logger.Options{Level: logger.LevelInfo, Stdout: out, Stderr: errBuf, GHAAuto: true})
	defer logger.Init(logger.Options{Level: logger.LevelInfo, GHAAuto: true})

	l := warnGateLogger(logger.LevelInfo)
	for i := range maxWarnings + 1 {
		l.Warn("warning %d", i+1)
	}
	for range 4 {
		l.Warn("warning 1")
	}

	require.Error(t, checkWarningsGate())

	recap := errBuf.String()
	assert.Contains(t, recap, "16 distinct warnings emitted (threshold: 15), 20 emitted in total")
	assert.Contains(t, recap, "warning 1 (emitted 5 times)")
	assert.Contains(t, recap, "2. warning 2")
	assert.NotContains(t, recap, "warning 2 (emitted")
}

// TestWarningsGateIgnoresFilteredWarnings verifies that warnings suppressed
// by the log level do not count against the budget — only what the user
// actually saw is gated.
func TestWarningsGateIgnoresFilteredWarnings(t *testing.T) {
	t.Serial()
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()

	silenced := warnGateLogger(logger.LevelError)
	for i := 0; i < maxWarnings*2; i++ {
		silenced.Warn("suppressed %d", i)
	}
	require.Equal(t, int64(0), logger.WarnCount())

	assert.NoError(t, checkWarningsGate())
}

// TestWarningsGateReprintsEveryWarning verifies that the gate failure lists
// every warning it counted. A bare count is unactionable: the reader has to
// scroll back and guess which output was to blame, and the loudest lines in a
// build log (the watchdog's STALLED banner) never reach this counter at all.
func TestWarningsGateReprintsEveryWarning(t *testing.T) {
	t.Serial()
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()
	t.Setenv("GITHUB_ACTIONS", "false")

	out, errBuf := &strings.Builder{}, &strings.Builder{}
	logger.Init(logger.Options{Level: logger.LevelInfo, Stdout: out, Stderr: errBuf, GHAAuto: true})
	defer logger.Init(logger.Options{Level: logger.LevelInfo, GHAAuto: true})

	l := warnGateLogger(logger.LevelInfo)
	for i := 0; i < maxWarnings+2; i++ {
		l.Warn("distinct warning number %d", i+1)
	}

	err := checkWarningsGate()
	require.Error(t, err)

	recap := errBuf.String()
	assert.Contains(t, recap, "17 distinct warnings emitted (threshold: 15)")
	for i := 0; i < maxWarnings+2; i++ {
		assert.Contains(t, recap, fmt.Sprintf("distinct warning number %d", i+1))
	}
}

// TestWarningsGateRecapAnnotatesInGHA verifies that in GitHub Actions the
// recap rides a SINGLE ::error annotation with its newlines escaped, so the whole
// list survives in the annotation instead of truncating to its leading line.
func TestWarningsGateRecapAnnotatesInGHA(t *testing.T) {
	t.Serial()
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()
	t.Setenv("GITHUB_ACTIONS", "true")

	out, errBuf := &strings.Builder{}, &strings.Builder{}
	logger.Init(logger.Options{Level: logger.LevelInfo, Stdout: out, Stderr: errBuf, GHAAuto: true})
	defer logger.Init(logger.Options{Level: logger.LevelInfo, GHAAuto: true})

	l := warnGateLogger(logger.LevelInfo)
	for i := 0; i < maxWarnings+1; i++ {
		l.Warn("annotated warning %d", i+1)
	}

	require.Error(t, checkWarningsGate())

	annotations := out.String()
	assert.Equal(t, 1, strings.Count(annotations, "::error"))
	assert.Contains(t, annotations, "%0A") // newlines escaped, not truncated
	assert.Contains(t, annotations, "annotated warning 16")
}

// TestWarningsRecapReportsUnrecorded verifies that warnings past the
// retention cap are reported as a count rather than silently dropped.
func TestWarningsRecapReportsUnrecorded(t *testing.T) {
	t.Serial()
	recorded := make([]logger.Warning, logger.MaxRecordedWarnings)
	for i := range recorded {
		recorded[i] = logger.Warning{Message: fmt.Sprintf("recorded %d", i), Count: 1}
	}

	recap := warningsRecap(int64(logger.MaxRecordedWarnings+3), int64(logger.MaxRecordedWarnings+3), recorded)

	assert.Contains(t, recap, fmt.Sprintf("... and 3 more (only the first %d are recorded)", logger.MaxRecordedWarnings))
}
