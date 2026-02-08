package cmd

import (
	"strings"
	"testing"
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
		if !strings.Contains(result, tc.contains) {
			t.Errorf("colorPct(%v) = %q, expected to contain %q", tc.pct, result, tc.contains)
		}
		if !strings.Contains(result, colorReset) {
			t.Errorf("colorPct(%v) should contain reset sequence", tc.pct)
		}
	}
}

func TestColorPctCustomFormat(t *testing.T) {
	result := colorPct(ColorPct{Pct: 50, Format: "%.0f%%"})
	if !strings.Contains(result, "50%") {
		t.Errorf("expected '50%%' in output, got %q", result)
	}
}

func TestColorPctBoundaries(t *testing.T) {
	// Test that values outside 0-100 don't crash
	_ = colorPct(ColorPct{Pct: -10})
	_ = colorPct(ColorPct{Pct: 150})
}

func TestWarn(t *testing.T) {
	result := warn("test message")
	if !strings.Contains(result, "WARNING:") {
		t.Error("warn should contain 'WARNING:'")
	}
	if !strings.Contains(result, "test message") {
		t.Error("warn should contain the message")
	}
	if !strings.Contains(result, colorYellow) {
		t.Error("warn should contain yellow color code")
	}
	if !strings.Contains(result, colorReset) {
		t.Error("warn should contain reset code")
	}
}

func TestColorConstants(t *testing.T) {
	// Verify color constants have correct RGB values
	if colorGreen != "\033[38;2;0;255;0m" {
		t.Errorf("colorGreen should be 0,255,0, got %q", colorGreen)
	}
	if colorRed != "\033[38;2;255;0;0m" {
		t.Errorf("colorRed should be 255,0,0, got %q", colorRed)
	}
	if colorYellow != "\033[38;2;255;255;0m" {
		t.Errorf("colorYellow should be 255,255,0, got %q", colorYellow)
	}
	if colorFail != "\033[38;2;255;128;128m" {
		t.Errorf("colorFail should be 255,128,128, got %q", colorFail)
	}
	if colorPass != colorGreen {
		t.Errorf("colorPass should be colorGreen, got %q", colorPass)
	}
}
