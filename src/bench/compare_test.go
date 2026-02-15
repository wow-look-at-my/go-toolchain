package bench

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestCompareNilReports(t *testing.T) {
	comp := Compare(nil, nil)
	if len(comp.Packages) != 0 {
		t.Error("expected empty packages for nil reports")
	}
}

func TestCompareNoPrevious(t *testing.T) {
	current := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo-8", NsPerOp: 1000}},
		},
	}

	comp := Compare(current, nil)
	if len(comp.Packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(comp.Packages))
	}

	deltas := comp.Packages["pkg"]
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}

	if deltas[0].Previous != nil {
		t.Error("expected nil previous when no previous report")
	}
	if deltas[0].NsPerOpDelta != 0 {
		t.Errorf("expected 0 delta, got %f", deltas[0].NsPerOpDelta)
	}
}

func TestCompareWithPrevious(t *testing.T) {
	current := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo-8", NsPerOp: 900, BytesPerOp: 200, AllocsPerOp: 4}},
		},
	}
	previous := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo-8", NsPerOp: 1000, BytesPerOp: 250, AllocsPerOp: 5}},
		},
	}

	comp := Compare(current, previous)
	deltas := comp.Packages["pkg"]
	if len(deltas) != 1 {
		t.Fatalf("expected 1 delta, got %d", len(deltas))
	}

	d := deltas[0]
	if d.Previous == nil {
		t.Fatal("expected non-nil previous")
	}

	// 900 vs 1000 = -10%
	if d.NsPerOpDelta < -10.1 || d.NsPerOpDelta > -9.9 {
		t.Errorf("expected ~-10%% ns/op delta, got %f", d.NsPerOpDelta)
	}

	// 200 vs 250 = -20%
	if d.BytesDelta < -20.1 || d.BytesDelta > -19.9 {
		t.Errorf("expected ~-20%% bytes delta, got %f", d.BytesDelta)
	}

	// 4 vs 5 = -20%
	if d.AllocsDelta < -20.1 || d.AllocsDelta > -19.9 {
		t.Errorf("expected ~-20%% allocs delta, got %f", d.AllocsDelta)
	}
}

func TestCompareRegression(t *testing.T) {
	current := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo-8", NsPerOp: 1100}},
		},
	}
	previous := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo-8", NsPerOp: 1000}},
		},
	}

	comp := Compare(current, previous)
	d := comp.Packages["pkg"][0]

	// 1100 vs 1000 = +10%
	if d.NsPerOpDelta < 9.9 || d.NsPerOpDelta > 10.1 {
		t.Errorf("expected ~+10%% delta, got %f", d.NsPerOpDelta)
	}
}

func TestCompareNewBenchmark(t *testing.T) {
	current := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {
				{Name: "BenchmarkFoo-8", NsPerOp: 1000},
				{Name: "BenchmarkNew-8", NsPerOp: 500},
			},
		},
	}
	previous := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo-8", NsPerOp: 1000}},
		},
	}

	comp := Compare(current, previous)
	deltas := comp.Packages["pkg"]
	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(deltas))
	}

	// Find the new benchmark
	var newDelta *Delta
	for i := range deltas {
		if deltas[i].Name == "BenchmarkNew" {
			newDelta = &deltas[i]
			break
		}
	}

	if newDelta == nil {
		t.Fatal("expected to find BenchmarkNew delta")
	}
	if newDelta.Previous != nil {
		t.Error("expected nil previous for new benchmark")
	}
}

func TestHasDeltas(t *testing.T) {
	// No previous data
	comp := &Comparison{
		Packages: map[string][]Delta{
			"pkg": {{Name: "Foo", Previous: nil}},
		},
	}
	if comp.HasDeltas() {
		t.Error("expected HasDeltas() false when no previous data")
	}

	// With previous data
	prev := BenchmarkResult{Name: "Foo"}
	comp.Packages["pkg"][0].Previous = &prev
	if !comp.HasDeltas() {
		t.Error("expected HasDeltas() true when previous data exists")
	}
}

func TestStripCPUSuffix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"BenchmarkFoo-8", "BenchmarkFoo"},
		{"BenchmarkFoo-16", "BenchmarkFoo"},
		{"BenchmarkFoo", "BenchmarkFoo"},
		{"Benchmark-Hyphen-8", "Benchmark-Hyphen"},
	}

	for _, tc := range tests {
		result := stripCPUSuffix(tc.input)
		if result != tc.expected {
			t.Errorf("stripCPUSuffix(%q) = %q, expected %q", tc.input, result, tc.expected)
		}
	}
}

func TestComparisonPrint(t *testing.T) {
	comp := &Comparison{
		Packages: map[string][]Delta{
			"example.com/pkg": {
				{
					Name:    "BenchmarkFoo",
					Current: BenchmarkResult{NsPerOp: 1000, BytesPerOp: 256, AllocsPerOp: 4},
				},
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	comp.Print()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "pkg") {
		t.Error("expected output to contain package name")
	}
	if !strings.Contains(output, "Foo") {
		t.Error("expected output to contain benchmark name")
	}
}

func TestComparisonPrintEmpty(t *testing.T) {
	comp := &Comparison{
		Packages: map[string][]Delta{},
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	comp.Print()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "no benchmarks") {
		t.Error("expected 'no benchmarks' message")
	}
}

func TestFormatDelta(t *testing.T) {
	// No previous - should show dash
	result := formatDelta(0, false)
	if !strings.Contains(result, "-") {
		t.Errorf("expected dash for no previous, got %q", result)
	}

	// Improvement (negative)
	result = formatDelta(-15.5, true)
	if !strings.Contains(result, "-15.5%") {
		t.Errorf("expected -15.5%% in result, got %q", result)
	}

	// Regression (positive)
	result = formatDelta(10.2, true)
	if !strings.Contains(result, "+10.2%") {
		t.Errorf("expected +10.2%% in result, got %q", result)
	}
}
