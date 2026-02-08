package cmd

import (
	"fmt"
	"math"
)

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

// hslToRGB converts HSL to RGB. h is in degrees [0,360), s and l are [0,1].
func hslToRGB(h, s, l float64) (r, g, b uint8) {
	c := (1 - math.Abs(2*l-1)) * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := l - c/2

	var r1, g1, b1 float64
	switch {
	case h < 60:
		r1, g1, b1 = c, x, 0
	case h < 120:
		r1, g1, b1 = x, c, 0
	case h < 180:
		r1, g1, b1 = 0, c, x
	case h < 240:
		r1, g1, b1 = 0, x, c
	case h < 300:
		r1, g1, b1 = x, 0, c
	default:
		r1, g1, b1 = c, 0, x
	}

	return uint8((r1 + m) * 255), uint8((g1 + m) * 255), uint8((b1 + m) * 255)
}

// colorPct formats a percentage with color based on value (red=0%, green=100%)
// Uses HSL hue rotation: 0° (red) → 60° (yellow) → 120° (green)
func colorPct(p ColorPct) string {
	format := p.Format
	if format == "" {
		format = "%6.1f%%"
	}
	// Map 0-100% to hue 0-120° (red to green through yellow)
	hue := float64(p.Pct) * 1.2 // 0% = 0°, 100% = 120°
	r, g, b := hslToRGB(hue, 1.0, 0.5)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm"+format+colorReset, r, g, b, p.Pct)
}

// warn formats a warning message in yellow
func warn(msg string) string {
	return colorYellow + "WARNING: " + msg + colorReset
}
