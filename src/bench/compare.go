package bench

import (
	"fmt"
	"sort"
	"strings"
)

// Comparison holds benchmark deltas between two reports
type Comparison struct {
	Packages       map[string][]Delta
	PreviousCommit string
}

// Delta represents the change in a single benchmark
type Delta struct {
	Name         string
	Current      BenchmarkResult
	Previous     *BenchmarkResult
	NsPerOpDelta float64 // percentage change, negative = faster
	BytesDelta   float64 // percentage change
	AllocsDelta  float64 // percentage change
}

// Compare calculates deltas between current and previous benchmark reports
func Compare(current, previous *BenchmarkReport) *Comparison {
	comp := &Comparison{
		Packages: make(map[string][]Delta),
	}

	if current == nil {
		return comp
	}

	// Build lookup map from previous results
	prevMap := make(map[string]map[string]BenchmarkResult)
	if previous != nil {
		for pkg, results := range previous.Packages {
			if prevMap[pkg] == nil {
				prevMap[pkg] = make(map[string]BenchmarkResult)
			}
			for _, r := range results {
				// Normalize name by stripping -N suffix
				name := stripCPUSuffix(r.Name)
				prevMap[pkg][name] = r
			}
		}
	}

	// Calculate deltas for each current result
	for pkg, results := range current.Packages {
		for _, curr := range results {
			name := stripCPUSuffix(curr.Name)
			delta := Delta{
				Name:    name,
				Current: curr,
			}

			if pkgMap, ok := prevMap[pkg]; ok {
				if prev, ok := pkgMap[name]; ok {
					delta.Previous = &prev
					if prev.NsPerOp > 0 {
						delta.NsPerOpDelta = ((curr.NsPerOp - prev.NsPerOp) / prev.NsPerOp) * 100
					}
					if prev.BytesPerOp > 0 {
						delta.BytesDelta = ((float64(curr.BytesPerOp) - float64(prev.BytesPerOp)) / float64(prev.BytesPerOp)) * 100
					}
					if prev.AllocsPerOp > 0 {
						delta.AllocsDelta = ((float64(curr.AllocsPerOp) - float64(prev.AllocsPerOp)) / float64(prev.AllocsPerOp)) * 100
					}
				}
			}

			comp.Packages[pkg] = append(comp.Packages[pkg], delta)
		}
	}

	return comp
}

// Print outputs the comparison in a formatted table
func (c *Comparison) Print() {
	if len(c.Packages) == 0 {
		fmt.Println("     (no benchmarks to compare)")
		return
	}

	// Sort packages
	pkgNames := make([]string, 0, len(c.Packages))
	for pkg := range c.Packages {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)

	fmt.Println("        time/op       delta     alloc/op   allocs/op  name")
	for _, pkg := range pkgNames {
		deltas := c.Packages[pkg]
		// Sort by ns/op (fastest first)
		sort.Slice(deltas, func(i, j int) bool {
			return deltas[i].Current.NsPerOp < deltas[j].Current.NsPerOp
		})

		// Print package header
		shortPkg := pkg
		if idx := strings.LastIndex(pkg, "/"); idx >= 0 {
			shortPkg = pkg[idx+1:]
		}
		fmt.Printf("\033[1m%s\033[0m\n", shortPkg)

		for _, d := range deltas {
			name := d.Name
			if strings.HasPrefix(name, "Benchmark") {
				name = name[9:]
			}

			timeStr := formatBenchTime(d.Current.NsPerOp)
			allocStr := formatBenchBytes(d.Current.BytesPerOp)
			deltaStr := formatDelta(d.NsPerOpDelta, d.Previous != nil)

			fmt.Printf("  %12s  %10s  %10s  %9d  %s\n",
				timeStr, deltaStr, allocStr, d.Current.AllocsPerOp, name)
		}
	}
}

// HasDeltas returns true if any benchmarks have previous data to compare
func (c *Comparison) HasDeltas() bool {
	for _, deltas := range c.Packages {
		for _, d := range deltas {
			if d.Previous != nil {
				return true
			}
		}
	}
	return false
}

func stripCPUSuffix(name string) string {
	if idx := strings.LastIndex(name, "-"); idx > 0 {
		return name[:idx]
	}
	return name
}

func formatDelta(pct float64, hasPrevious bool) string {
	if !hasPrevious {
		return "     -"
	}

	// Color: green for improvement (negative), red for regression (positive)
	var color string
	if pct < -1 {
		color = "\033[38;2;0;255;0m" // green
	} else if pct > 1 {
		color = "\033[38;2;255;128;128m" // red
	} else {
		color = "\033[38;2;128;128;128m" // gray for ~0
	}

	sign := ""
	if pct > 0 {
		sign = "+"
	}
	return fmt.Sprintf("%s%s%.1f%%\033[0m", color, sign, pct)
}
