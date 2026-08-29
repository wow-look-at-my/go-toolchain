package test

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

const (
	colorReset   = "\033[0m"
	fgReset      = "\033[39m" // reset foreground only, preserve background
	colorDimCyan = "\033[38;2;100;160;160m"
)

// cwd is cached working directory for link resolution
var cwd string

func init() {
	cwd, _ = os.Getwd()
}

// osc8Link wraps text in an OSC8 terminal hyperlink
func osc8Link(url, text string) string {
	return fmt.Sprintf("\033]8;;%s\033\\%s\033]8;;\033\\", url, text)
}

// resolveToFileURL converts an import path to a file:// URL
func resolveToFileURL(importPath string, line int) string {
	// Strip module prefix to get relative path
	parts := strings.Split(importPath, "/")
	for i := range parts {
		candidate := filepath.Join(parts[i:]...)
		if _, err := os.Stat(candidate); err == nil {
			abs := candidate
			if !filepath.IsAbs(candidate) {
				abs = filepath.Join(cwd, candidate)
			}
			if line > 0 {
				return fmt.Sprintf("file://%s:%d", abs, line)
			}
			return "file://" + abs
		}
	}
	return ""
}

// hsvToRGB converts HSV to RGB. h is in degrees, s and v are unit fractions.
func hsvToRGB(h, s, v float64) (r, g, b uint8) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c

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

// dimText returns ANSI code for dimmed white/grey text based on value
func dimText(value float64) string {
	v := uint8(value * 255)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", v, v, v)
}

func sortByUncovered[T ICoverageItem](items []T) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Uncovered() != items[j].Uncovered() {
			return items[i].Uncovered() > items[j].Uncovered()
		}
		return items[i].Name() < items[j].Name()
	})
}

// funcWithPath holds a function and its containing file for display
type funcWithPath struct {
	fn   *FuncCoverage
	file *FileCoverage
}

// shortFile returns just the last directory + filename from an import path
func shortFile(importPath string) string {
	parts := strings.Split(importPath, "/")
	if len(parts) >= 2 {
		return parts[len(parts)-2] + "/" + parts[len(parts)-1]
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return importPath
}

// colorGain formats a gain percentage with green color (higher = more green)
func colorGain(gain float32) string {
	hue := 120 - float64(gain)*120
	if hue < 0 {
		hue = 0
	}
	r, g, b := hsvToRGB(hue, 0.8, 0.9)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm%4.1f%%%s", r, g, b, gain, fgReset)
}

// Print prints coverage as a flat ranked list of functions to test,
// sorted by potential gain (uncovered lines / total statements).
// Functions are split into UNTESTED (nothing covered) and PARTIAL groups.
func (r Report) Print() {
	var totalStatements int
	for i := range r.Packages {
		totalStatements += r.Packages[i].Statements
	}

	// Collect all functions with uncovered statements
	var allFuncs []funcWithPath
	for i := range r.Packages {
		for j := range r.Packages[i].Files {
			file := &r.Packages[i].Files[j]
			for k := range file.Functions {
				fn := &file.Functions[k]
				if fn.Uncovered() > 0 {
					allFuncs = append(allFuncs, funcWithPath{fn: fn, file: file})
				}
			}
		}
	}

	// Sort by uncovered count (descending), then by name
	sort.Slice(allFuncs, func(i, j int) bool {
		if allFuncs[i].fn.Uncovered() != allFuncs[j].fn.Uncovered() {
			return allFuncs[i].fn.Uncovered() > allFuncs[j].fn.Uncovered()
		}
		return allFuncs[i].fn.Function < allFuncs[j].fn.Function
	})

	// Split into untested (nothing covered) and partial
	var untested, partial []funcWithPath
	for _, f := range allFuncs {
		if f.fn.Covered == 0 {
			untested = append(untested, f)
		} else {
			partial = append(partial, f)
		}
	}

	if len(untested) > 5 {
		untested = untested[:5]
	}
	if len(partial) > 5 {
		partial = partial[:5]
	}

	printTargetGroup(untested, "UNTESTED (0% covered — one test likely covers most lines):", totalStatements)
	if len(untested) > 0 && len(partial) > 0 {
		logger.Info("")
	}
	printTargetGroup(partial, "PARTIAL (need specific branches/inputs):", totalStatements)
}

func printTargetGroup(funcs []funcWithPath, header string, totalStatements int) {
	if len(funcs) == 0 {
		return
	}

	dim := dimText(0.6)
	logger.Info("  %s%s%s", dim, header, colorReset)

	for _, f := range funcs {
		var gain float32
		if totalStatements > 0 {
			gain = float32(f.fn.Uncovered()) / float32(totalStatements) * 100
		}

		location := fmt.Sprintf("%s:%d", shortFile(f.file.File), f.fn.FuncLine)

		name := f.fn.Function
		if os.Getenv("CI") == "" {
			if url := resolveToFileURL(f.file.File, f.fn.FuncLine); url != "" {
				name = osc8Link(url, name)
			}
		}

		logger.Info("   %s  %s%3d stmts%s  %-28s %s",
			colorGain(gain),
			dim, f.fn.Uncovered(), fgReset,
			location,
			name,
		)
	}
}
