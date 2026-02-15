package cmd

import (
	"strings"
	"testing"
	"github.com/stretchr/testify/assert"

)

func TestColorPct(t *testing.T) {
	tests := []struct {
		pct      float32
		contains string
	}{
		{0, "\033[38;2;255;0;0m"},   // Red for 0%
		{100, "\033[38;2;0;255;0m"}, // Green for 100%
		{50, "50.0%"},               // Contains the percentage
	}

	for _, tc := range tests {
		result := colorPct(ColorPct{Pct: tc.pct})
		assert.Contains(t, result, tc.contains)
		assert.Contains(t, result, colorReset)
	}
}

func TestColorPctCustomFormat(t *testing.T) {
	result := colorPct(ColorPct{Pct: 50, Format: "%.0f%%"})
	assert.Contains(t, result, "50%")
}

func TestColorPctBoundaries(t *testing.T) {
	// Test that values outside 0-100 don't crash
	_ = colorPct(ColorPct{Pct: -10})
	_ = colorPct(ColorPct{Pct: 150})
}

func TestWarn(t *testing.T) {
	result := warn("test message")
	assert.Contains(t, result, "WARNING:")
	assert.Contains(t, result, "test message")
	assert.Contains(t, result, colorYellow)
	assert.Contains(t, result, colorReset)
}

func TestColorConstants(t *testing.T) {
	// Verify color constants have correct RGB values
	assert.Equal(t, "\033[38;2;0;255;0m", colorGreen)
	assert.Equal(t, "\033[38;2;255;0;0m", colorRed)
	assert.Equal(t, "\033[38;2;255;255;0m", colorYellow)
	assert.Equal(t, "\033[38;2;255;128;128m", colorFail)
	assert.Equal(t, colorGreen, colorPass)
}
