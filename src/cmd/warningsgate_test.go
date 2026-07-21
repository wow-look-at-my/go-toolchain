package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// warnGateLogger returns a logger at the given level that emits into
// discarded buffers, so gate tests can drive the process-wide warning
// counter without printing to the test output.
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
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()

	l := warnGateLogger(logger.LevelInfo)
	for i := 0; i < maxWarnings; i++ {
		l.Warn("warning %d", i+1)
	}
	require.Equal(t, int64(maxWarnings), logger.WarnCount())

	assert.NoError(t, checkWarningsGate())
}

// TestWarningsGateOverThreshold verifies that one warning past the budget
// (16 with the threshold at 15) fails the build with a message naming both
// the count and the threshold.
func TestWarningsGateOverThreshold(t *testing.T) {
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()

	l := warnGateLogger(logger.LevelInfo)
	for i := 0; i < maxWarnings+1; i++ {
		l.Warn("warning %d", i+1)
	}
	require.Equal(t, int64(maxWarnings+1), logger.WarnCount())

	err := checkWarningsGate()
	require.Error(t, err)
	assert.Equal(t, "build failed: 16 warnings emitted (threshold: 15)", err.Error())
}

// TestWarningsGateIgnoresFilteredWarnings verifies that warnings suppressed
// by the log level do not count against the budget — only what the user
// actually saw is gated.
func TestWarningsGateIgnoresFilteredWarnings(t *testing.T) {
	logger.ResetWarnCount()
	defer logger.ResetWarnCount()

	silenced := warnGateLogger(logger.LevelError)
	for i := 0; i < maxWarnings*2; i++ {
		silenced.Warn("suppressed %d", i)
	}
	require.Equal(t, int64(0), logger.WarnCount())

	assert.NoError(t, checkWarningsGate())
}
