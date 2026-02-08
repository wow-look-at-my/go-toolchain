package cmd

import (
	"fmt"

	"github.com/mazznoer/colorgrad"
)

var coverageGrad, _ = colorgrad.NewGradient().HtmlColors("red", "lime").Build()

const colorReset = "\033[0m"
const colorYellow = "\033[38;2;255;255;0m"
const colorGreen = "\033[38;2;0;255;0m"
const colorRed = "\033[38;2;255;0;0m"
const colorPass = colorGreen
const colorFail = "\033[38;2;255;128;128m" // softer red for readability

type ColorPct struct {
	Pct    float32
	Format string
}

// colorPct formats a percentage with color based on value (red=0%, green=100%)
func colorPct(p ColorPct) string {
	format := p.Format
	if format == "" {
		format = "%6.1f%%"
	}
	c := coverageGrad.At(float64(p.Pct) / 100)
	r := uint8(c.R * 255)
	g := uint8(c.G * 255)
	b := uint8(c.B * 255)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm"+format+colorReset, r, g, b, p.Pct)
}

// warn formats a warning message in yellow
func warn(msg string) string {
	return colorYellow + "WARNING: " + msg + colorReset
}
