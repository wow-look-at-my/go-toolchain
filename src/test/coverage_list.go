package test

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	colorReset = "\033[0m"
	fgReset    = "\033[39m" // reset foreground only, preserve background
)

// cwd is cached working directory for link resolution
var cwd string

func init() {
	cwd, _ = os.Getwd()
}

// osc8Link wraps text in an OSC 8 hyperlink
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

// hsvToRGB converts HSV to RGB. h is in degrees [0,360), s and v are [0,1].
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

// colorPct formats a percentage with color based on coverage (red=0%, green=100%)
func colorPct(pct float32, saturation, value float64) string {
	hue := float64(pct) * 1.2 // 0% = 0°, 100% = 120°
	r, g, b := hsvToRGB(hue, saturation, value)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm%5.1f%%%s", r, g, b, pct, fgReset)
}

// dimText returns ANSI code for dimmed white/grey text based on value
func dimText(value float64) string {
	v := uint8(value * 255)
	return fmt.Sprintf("\033[38;2;%d;%d;%dm", v, v, v)
}

// depthToStyle returns saturation and value for a given depth
func depthToStyle(depth int) (saturation, value float64) {
	switch depth {
	case 0:
		return 1.0, 1.0
	case 1:
		return 0.5, 0.75
	default:
		return 0.35, 0.5
	}
}

const bold = "\033[1m"

func printItem(c ICoverageItem, depth int) {
	pad := ""
	for i := 0; i < depth; i++ {
		pad += "  "
	}

	name := c.Name()
	if url := resolveToFileURL(c.ImportPath(), c.Line()); url != "" {
		name = osc8Link(url, name)
	}

	sat, val := depthToStyle(depth)
	dim := dimText(val)
	prefix := ""
	if depth == 0 {
		prefix = bold
	}
	if c.Uncovered() == 0 && c.Pct() == 0 {
		fmt.Printf("%s%s       ∅       %s%s%s\n", prefix, dim, pad, name, colorReset)
	} else {
		fmt.Printf("%s  %s  %s%3d  %s%s%s\n", prefix, colorPct(c.Pct(), sat, val), dim, c.Uncovered(), pad, name, colorReset)
	}
}

// PrintOptions controls the verbosity of coverage output
type PrintOptions struct {
	ShowFiles bool
	ShowFuncs bool
	Verbose   bool // Show all entries, including those with 0 uncovered
}

func sortByUncovered[T ICoverageItem](items []T) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Uncovered() != items[j].Uncovered() {
			return items[i].Uncovered() > items[j].Uncovered()
		}
		return items[i].Name() < items[j].Name()
	})
}

// Print prints coverage in package > file > function hierarchy
func (r Report) Print(opts PrintOptions) {
	sortByUncovered(r.Packages)
	fmt.Println("     cov  miss  name")
	for _, pkg := range r.Packages {
		printItem(pkg, 0)
		if opts.ShowFiles {
			sortByUncovered(pkg.Files)
			for _, f := range pkg.Files {
				if !opts.Verbose && f.Uncovered() == 0 {
					continue
				}
				printItem(f, 1)
				if opts.ShowFuncs {
					sortByUncovered(f.Functions)
					for _, fn := range f.Functions {
						if !opts.Verbose && fn.Uncovered() == 0 {
							continue
						}
						printItem(fn, 2)
					}
				}
			}
		}
	}
}
