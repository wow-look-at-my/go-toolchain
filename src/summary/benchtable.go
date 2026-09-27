package summary

import (
	"fmt"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/bench"
)

func writeBenchmarkTable(sb *strings.Builder, report *bench.BenchmarkReport, comp *bench.Comparison) {
	hasComparison := comp != nil && comp.HasDeltas()
	deltaMap := buildDeltaMap(comp)

	// Sort packages for stable output
	pkgNames := make([]string, 0, len(report.Packages))
	for pkg := range report.Packages {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)

	for _, pkg := range pkgNames {
		results := report.Packages[pkg]
		sort.Slice(results, func(i, j int) bool {
			return results[i].NsPerOp < results[j].NsPerOp
		})

		short := shortPkg(pkg)
		sb.WriteString(fmt.Sprintf("<details>\n<summary>Benchmarks: %s</summary>\n\n", short))

		if hasComparison {
			sb.WriteString("| Benchmark | time/op | vs previous | alloc/op | allocs/op |\n")
			sb.WriteString("|-----------|--------:|------------:|---------:|----------:|\n")
		} else {
			sb.WriteString("| Benchmark | time/op | alloc/op | allocs/op |\n")
			sb.WriteString("|-----------|--------:|---------:|----------:|\n")
		}

		for _, b := range results {
			name := benchDisplayName(b.Name, short)
			timeStr := formatBenchTime(b.NsPerOp)
			allocStr := formatBenchBytes(b.BytesPerOp)

			if hasComparison {
				deltaStr := ""
				key := pkg + "/" + stripCPUSuffix(b.Name)
				if d, ok := deltaMap[key]; ok {
					deltaStr = formatBenchDelta(d.NsPerOpDelta)
				}
				sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %d |\n",
					name, timeStr, deltaStr, allocStr, b.AllocsPerOp))
			} else {
				sb.WriteString(fmt.Sprintf("| %s | %s | %s | %d |\n",
					name, timeStr, allocStr, b.AllocsPerOp))
			}
		}

		if hasComparison && comp.PreviousCommit != "" {
			sb.WriteString(fmt.Sprintf("\n_Compared against `%s`_\n", comp.PreviousCommit))
		}

		sb.WriteString("\n</details>\n\n")
	}
}

func buildDeltaMap(comp *bench.Comparison) map[string]bench.Delta {
	m := make(map[string]bench.Delta)
	if comp == nil {
		return m
	}
	for pkg, deltas := range comp.Packages {
		for _, d := range deltas {
			key := pkg + "/" + d.Name
			m[key] = d
		}
	}
	return m
}

func benchDisplayName(name, shortPkg string) string {
	n := name
	if strings.HasPrefix(n, "Benchmark") {
		n = n[9:]
	}
	// Strip the CPU-count suffix (e.g. "-N")
	if idx := strings.LastIndex(n, "-"); idx > 0 {
		// Only strip if suffix is all digits
		suffix := n[idx+1:]
		allDigits := true
		for _, c := range suffix {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			n = n[:idx]
		}
	}
	return n
}

func stripCPUSuffix(name string) string {
	if idx := strings.LastIndex(name, "-"); idx > 0 {
		return name[:idx]
	}
	return name
}

func formatBenchTime(ns float64) string {
	switch {
	case ns >= 1e9:
		return fmt.Sprintf("%.2f s", ns/1e9)
	case ns >= 1e6:
		return fmt.Sprintf("%.2f ms", ns/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.2f us", ns/1e3)
	default:
		return fmt.Sprintf("%.1f ns", ns)
	}
}

func formatBenchBytes(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

func formatBenchDelta(pct float64) string {
	if pct < -1 {
		return fmt.Sprintf("%.1f%% :arrow_down:", pct)
	} else if pct > 1 {
		return fmt.Sprintf("+%.1f%% :arrow_up:", pct)
	}
	return "~0%"
}
