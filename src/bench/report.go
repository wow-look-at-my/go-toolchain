package bench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// BenchmarkResult holds parsed benchmark data
type BenchmarkResult struct {
	Name       string  `json:"name"`
	Package    string  `json:"package"`
	Iterations int64   `json:"iterations"`
	NsPerOp    float64 `json:"ns_per_op"`
	BytesPerOp int64   `json:"bytes_per_op"`
	AllocsPerOp int64  `json:"allocs_per_op"`
}

// BenchmarkReport holds all benchmark results grouped by package
type BenchmarkReport struct {
	Packages map[string][]BenchmarkResult `json:"packages"`
}

// testEvent matches go test -json output
type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Output  string `json:"Output"`
}

// benchPattern matches benchmark output lines:
// BenchmarkFoo-8     10000    123456 ns/op    1234 B/op    56 allocs/op
var benchPattern = regexp.MustCompile(
	`^(Benchmark\S+)\s+(\d+)\s+([\d.]+)\s+ns/op(?:\s+(\d+)\s+B/op)?(?:\s+(\d+)\s+allocs/op)?`,
)

// ParseBenchmarkOutput parses go test -json output into a BenchmarkReport
func ParseBenchmarkOutput(data []byte) (*BenchmarkReport, error) {
	report := &BenchmarkReport{
		Packages: make(map[string][]BenchmarkResult),
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		var event testEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue // skip malformed lines
		}

		if event.Action != "output" {
			continue
		}

		matches := benchPattern.FindStringSubmatch(strings.TrimSpace(event.Output))
		if matches == nil {
			continue
		}

		result := BenchmarkResult{
			Name:    matches[1],
			Package: event.Package,
		}

		if v, err := strconv.ParseInt(matches[2], 10, 64); err == nil {
			result.Iterations = v
		}
		if v, err := strconv.ParseFloat(matches[3], 64); err == nil {
			result.NsPerOp = v
		}
		if len(matches) > 4 && matches[4] != "" {
			if v, err := strconv.ParseInt(matches[4], 10, 64); err == nil {
				result.BytesPerOp = v
			}
		}
		if len(matches) > 5 && matches[5] != "" {
			if v, err := strconv.ParseInt(matches[5], 10, 64); err == nil {
				result.AllocsPerOp = v
			}
		}

		report.Packages[event.Package] = append(report.Packages[event.Package], result)
	}

	return report, scanner.Err()
}

// formatBenchTime formats nanoseconds into human-readable duration
func formatBenchTime(ns float64) string {
	switch {
	case ns >= 1e9:
		return fmt.Sprintf("%.2f s", ns/1e9)
	case ns >= 1e6:
		return fmt.Sprintf("%.2f ms", ns/1e6)
	case ns >= 1e3:
		return fmt.Sprintf("%.2f µs", ns/1e3)
	default:
		return fmt.Sprintf("%.1f ns", ns)
	}
}

// formatBenchBytes formats bytes into human-readable size
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

// Print outputs the benchmark report in a pretty format
func (r *BenchmarkReport) Print() {
	if len(r.Packages) == 0 {
		fmt.Println("     (no benchmarks found)")
		return
	}

	// Sort packages by name
	pkgNames := make([]string, 0, len(r.Packages))
	for pkg := range r.Packages {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)

	fmt.Println("        time/op      alloc/op   allocs/op  name")
	for _, pkg := range pkgNames {
		results := r.Packages[pkg]
		// Sort by ns/op (fastest first)
		sort.Slice(results, func(i, j int) bool {
			return results[i].NsPerOp < results[j].NsPerOp
		})

		// Print package header
		shortPkg := pkg
		if idx := strings.LastIndex(pkg, "/"); idx >= 0 {
			shortPkg = pkg[idx+1:]
		}
		fmt.Printf("\033[1m%s\033[0m\n", shortPkg)

		for _, b := range results {
			// Strip package prefix and Benchmark prefix from name
			name := b.Name
			if strings.HasPrefix(name, "Benchmark") {
				name = name[9:]
			}
			// Strip -N suffix (CPU count)
			if idx := strings.LastIndex(name, "-"); idx > 0 {
				name = name[:idx]
			}

			timeStr := formatBenchTime(b.NsPerOp)
			allocStr := formatBenchBytes(b.BytesPerOp)

			fmt.Printf("  %12s  %12s  %9d  %s\n",
				timeStr, allocStr, b.AllocsPerOp, name)
		}
	}
}

// HasResults returns true if any benchmarks were found
func (r *BenchmarkReport) HasResults() bool {
	return len(r.Packages) > 0
}

// ToBenchstat outputs the report in benchstat-compatible text format
func (r *BenchmarkReport) ToBenchstat() string {
	var sb strings.Builder

	// Sort packages for consistent output
	pkgNames := make([]string, 0, len(r.Packages))
	for pkg := range r.Packages {
		pkgNames = append(pkgNames, pkg)
	}
	sort.Strings(pkgNames)

	for _, pkg := range pkgNames {
		results := r.Packages[pkg]
		// Sort by name for consistent output
		sort.Slice(results, func(i, j int) bool {
			return results[i].Name < results[j].Name
		})

		for _, b := range results {
			// Format: BenchmarkName-N    iterations    ns/op    B/op    allocs/op
			sb.WriteString(fmt.Sprintf("%s\t%d\t%.2f ns/op", b.Name, b.Iterations, b.NsPerOp))
			if b.BytesPerOp > 0 || b.AllocsPerOp > 0 {
				sb.WriteString(fmt.Sprintf("\t%d B/op\t%d allocs/op", b.BytesPerOp, b.AllocsPerOp))
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}
