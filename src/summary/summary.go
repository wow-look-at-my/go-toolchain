package summary

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/bench"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

// SummaryData holds all data needed to generate the GitHub Step Summary.
type SummaryData struct {
	TestCases  []gotest.TestCaseResult
	Coverage   *gotest.Report
	Benchmarks *bench.BenchmarkReport
	BenchComp  *bench.Comparison
	Timeline   []TimelineEntry
}

// Write generates a markdown summary and appends it to $GITHUB_STEP_SUMMARY.
// It is a no-op when not running in CI or when the env var is unset.
func Write(data *SummaryData) error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}

	md := GenerateMarkdown(data)
	if md == "" {
		return nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open step summary: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(md); err != nil {
		return fmt.Errorf("write step summary: %w", err)
	}
	return nil
}

// GenerateMarkdown builds the full step summary markdown string.
func GenerateMarkdown(data *SummaryData) string {
	if data == nil {
		return ""
	}

	commitSHA := os.Getenv("GITHUB_SHA")
	repo := os.Getenv("GITHUB_REPOSITORY")
	modulePath := readModulePath()

	var sb strings.Builder

	// Header with coverage
	if data.Coverage != nil {
		sb.WriteString(fmt.Sprintf("## go-toolchain: %.1f%% coverage\n\n", data.Coverage.Total))
	} else {
		sb.WriteString("## go-toolchain\n\n")
	}

	// Test summary line
	if len(data.TestCases) > 0 {
		passed, failed, skipped := countTestStatuses(data.TestCases)
		parts := []string{fmt.Sprintf("**%d** passed", passed)}
		if failed > 0 {
			parts = append(parts, fmt.Sprintf("**%d** failed", failed))
		}
		if skipped > 0 {
			parts = append(parts, fmt.Sprintf("**%d** skipped", skipped))
		}
		sb.WriteString(strings.Join(parts, " | ") + "\n\n")
	}

	// Test cases table (collapsed)
	if len(data.TestCases) > 0 {
		writeTestTable(&sb, data.TestCases, commitSHA, repo, modulePath)
	}

	// Benchmark results
	if data.Benchmarks != nil && data.Benchmarks.HasResults() {
		writeBenchmarkTable(&sb, data.Benchmarks, data.BenchComp)
	}

	// Pipeline timeline Gantt chart
	if gantt := RenderGantt(data.Timeline); gantt != "" {
		sb.WriteString("<details>\n<summary>Pipeline Timeline</summary>\n\n")
		sb.WriteString(gantt)
		sb.WriteString("\n</details>\n\n")
	}

	return sb.String()
}

func countTestStatuses(cases []gotest.TestCaseResult) (passed, failed, skipped int) {
	for _, tc := range cases {
		switch tc.Status {
		case "pass":
			passed++
		case "fail":
			failed++
		case "skip":
			skipped++
		}
	}
	return
}

// sortTestCases sorts tests so that parent tests appear before their subtests,
// and subtests are grouped under their parent. This fixes ordering issues caused
// by Go's test runner reporting subtest results before the parent.
func sortTestCases(cases []gotest.TestCaseResult) {
	sort.SliceStable(cases, func(i, j int) bool {
		ri := rootTestFunc(cases[i].Test)
		rj := rootTestFunc(cases[j].Test)
		if ri != rj {
			return false // preserve original order between different root tests
		}
		// Same root: parent before subtests, subtests in original order
		iIsSub := strings.Contains(cases[i].Test, "/")
		jIsSub := strings.Contains(cases[j].Test, "/")
		if !iIsSub && jIsSub {
			return true // parent before subtest
		}
		if iIsSub && !jIsSub {
			return false // subtest after parent
		}
		return false // preserve original order among subtests
	})
}

// testFuncLocation caches parsed test function locations.
type testFuncLocation struct {
	file string // repo-relative file path
	line int
}

func writeTestTable(sb *strings.Builder, cases []gotest.TestCaseResult, commitSHA, repo, modulePath string) {
	// Build source location cache
	locCache := buildTestLocationCache(cases, modulePath)

	// Group tests by package, preserving order of first appearance
	pkgOrder := []string{}
	pkgCases := make(map[string][]gotest.TestCaseResult)
	for _, tc := range cases {
		if _, exists := pkgCases[tc.Package]; !exists {
			pkgOrder = append(pkgOrder, tc.Package)
		}
		pkgCases[tc.Package] = append(pkgCases[tc.Package], tc)
	}

	for _, pkg := range pkgOrder {
		pkgTests := pkgCases[pkg]
		sortTestCases(pkgTests)
		passed, failed, skipped := countTestStatuses(pkgTests)
		short := shortPkg(pkg)

		statusParts := []string{fmt.Sprintf("%d passed", passed)}
		if failed > 0 {
			statusParts = append(statusParts, fmt.Sprintf("%d failed", failed))
		}
		if skipped > 0 {
			statusParts = append(statusParts, fmt.Sprintf("%d skipped", skipped))
		}

		sb.WriteString(fmt.Sprintf("<details>\n<summary>%s (%s)</summary>\n\n",
			short, strings.Join(statusParts, ", ")))
		sb.WriteString("| Status | Test | Time |\n")
		sb.WriteString("|--------|------|-----:|\n")

		var totalElapsed float64
		for _, tc := range pkgTests {
			status := statusEmoji(tc.Status)
			testDisplay := formatTestNameWithLink(tc, commitSHA, repo, modulePath, locCache)
			timeStr := formatElapsed(tc.Elapsed)
			totalElapsed += tc.Elapsed

			sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n",
				status, testDisplay, timeStr))
		}

		sb.WriteString(fmt.Sprintf("| | **All %s Tests** | **%s** |\n", short, formatElapsed(totalElapsed)))

		sb.WriteString("\n</details>\n\n")
	}
}

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

func statusEmoji(status string) string {
	switch status {
	case "pass":
		return ":white_check_mark:"
	case "fail":
		return ":x:"
	case "skip":
		return ":fast_forward:"
	default:
		return ":question:"
	}
}

func shortPkg(pkg string) string {
	if i := strings.LastIndex(pkg, "/"); i >= 0 {
		return pkg[i+1:]
	}
	return pkg
}

// formatTestNameWithLink returns the test name (possibly as a link) with subtest indentation.
func formatTestNameWithLink(tc gotest.TestCaseResult, commitSHA, repo, modulePath string, cache map[string]testFuncLocation) string {
	name := tc.Test
	indent := ""
	if strings.Contains(name, "/") {
		indent = "&nbsp;&nbsp;&nbsp;&nbsp;"
	}

	linkURL := sourceURL(tc, commitSHA, repo, modulePath, cache)
	if linkURL != "" {
		return fmt.Sprintf("%s[%s](%s)", indent, name, linkURL)
	}
	return indent + name
}

// formatTestName returns the display name for a test (used in non-link contexts).
func formatTestName(name string) string {
	if idx := strings.Index(name, "/"); idx >= 0 {
		return "&nbsp;&nbsp;&nbsp;&nbsp;" + name
	}
	return name
}

func formatElapsed(elapsed float64) string {
	if elapsed >= 1.0 {
		return fmt.Sprintf("%.2fs", elapsed)
	}
	return fmt.Sprintf("%.3fs", elapsed)
}

// rootTestFunc extracts the top-level test function name from a possibly nested subtest.
// "TestFoo/bar/baz" → "TestFoo"
func rootTestFunc(name string) string {
	if idx := strings.Index(name, "/"); idx >= 0 {
		return name[:idx]
	}
	return name
}

func buildTestLocationCache(cases []gotest.TestCaseResult, modulePath string) map[string]testFuncLocation {
	cache := make(map[string]testFuncLocation)

	// Collect unique package+func pairs to look up
	type lookupKey struct {
		pkg      string
		funcName string
	}
	needed := make(map[lookupKey]bool)
	for _, tc := range cases {
		needed[lookupKey{tc.Package, rootTestFunc(tc.Test)}] = true
	}

	// Group by package to avoid re-walking
	pkgFuncs := make(map[string]map[string]bool)
	for k := range needed {
		if pkgFuncs[k.pkg] == nil {
			pkgFuncs[k.pkg] = make(map[string]bool)
		}
		pkgFuncs[k.pkg][k.funcName] = true
	}

	for pkg, funcs := range pkgFuncs {
		dir := pkgToDir(pkg, modulePath)
		if dir == "" {
			continue
		}
		locs := findTestFuncsInDir(dir, funcs)
		for funcName, loc := range locs {
			cacheKey := pkg + "." + funcName
			cache[cacheKey] = loc
		}
	}

	return cache
}

// pkgToDir converts a Go package import path to a repo-relative directory.
func pkgToDir(pkg, modulePath string) string {
	if modulePath != "" && strings.HasPrefix(pkg, modulePath) {
		rel := strings.TrimPrefix(pkg, modulePath)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return "."
		}
		return rel
	}
	// Fallback: try stripping common github.com/owner/repo prefix
	parts := strings.SplitN(pkg, "/", 4)
	if len(parts) >= 4 {
		return parts[3]
	}
	if len(parts) == 3 {
		return "."
	}
	return ""
}

// findTestFuncsInDir parses _test.go files in a directory and returns locations
// for the requested function names.
func findTestFuncsInDir(dir string, funcNames map[string]bool) map[string]testFuncLocation {
	result := make(map[string]testFuncLocation)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return result
	}

	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			if funcNames[fn.Name.Name] {
				pos := fset.Position(fn.Pos())
				result[fn.Name.Name] = testFuncLocation{
					file: path,
					line: pos.Line,
				}
			}
		}
	}

	return result
}

func sourceURL(tc gotest.TestCaseResult, commitSHA, repo, modulePath string, cache map[string]testFuncLocation) string {
	if commitSHA == "" || repo == "" {
		return ""
	}

	funcName := rootTestFunc(tc.Test)
	cacheKey := tc.Package + "." + funcName
	loc, ok := cache[cacheKey]
	if !ok {
		return ""
	}

	return fmt.Sprintf("https://github.com/%s/blob/%s/%s#L%d", repo, commitSHA, loc.file, loc.line)
}

func benchDisplayName(name, shortPkg string) string {
	n := name
	if strings.HasPrefix(n, "Benchmark") {
		n = n[9:]
	}
	// Strip CPU suffix (e.g. "-8")
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

// readModulePath reads the module path from go.mod in the current directory.
func readModulePath() string {
	return gomod.ReadModulePath()
}
