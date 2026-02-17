package bench

import (
	"bytes"
	"os"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestCompareNilReports(t *testing.T) {
	comp := Compare(nil, nil)
	assert.Equal(t, 0, len(comp.Packages))
}

func TestCompareNoPrevious(t *testing.T) {
	current := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo-8", NsPerOp: 1000}},
		},
	}

	comp := Compare(current, nil)
	require.Equal(t, 1, len(comp.Packages))

	deltas := comp.Packages["pkg"]
	require.Equal(t, 1, len(deltas))

	assert.Nil(t, deltas[0].Previous)
	assert.Equal(t, 0, deltas[0].NsPerOpDelta)
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
	require.Equal(t, 1, len(deltas))

	d := deltas[0]
	require.NotNil(t, d.Previous)

	// 900 vs 1000 = -10%
	assert.False(t, d.NsPerOpDelta < -10.1 || d.NsPerOpDelta > -9.9)

	// 200 vs 250 = -20%
	assert.False(t, d.BytesDelta < -20.1 || d.BytesDelta > -19.9)

	// 4 vs 5 = -20%
	assert.False(t, d.AllocsDelta < -20.1 || d.AllocsDelta > -19.9)
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
	assert.False(t, d.NsPerOpDelta < 9.9 || d.NsPerOpDelta > 10.1)
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
	require.Equal(t, 2, len(deltas))

	// Find the new benchmark
	var newDelta *Delta
	for i := range deltas {
		if deltas[i].Name == "BenchmarkNew" {
			newDelta = &deltas[i]
			break
		}
	}

	require.NotNil(t, newDelta)
	assert.Nil(t, newDelta.Previous)
}

func TestHasDeltas(t *testing.T) {
	// No previous data
	comp := &Comparison{
		Packages: map[string][]Delta{
			"pkg": {{Name: "Foo", Previous: nil}},
		},
	}
	assert.False(t, comp.HasDeltas())

	// With previous data
	prev := BenchmarkResult{Name: "Foo"}
	comp.Packages["pkg"][0].Previous = &prev
	assert.True(t, comp.HasDeltas())
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
		assert.Equal(t, tc.expected, result)
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

	assert.Contains(t, output, "pkg")
	assert.Contains(t, output, "Foo")
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

	assert.Contains(t, output, "no benchmarks")
}

func TestFormatDelta(t *testing.T) {
	// No previous - should show dash
	result := formatDelta(0, false)
	assert.Contains(t, result, "-")

	// Improvement (negative)
	result = formatDelta(-15.5, true)
	assert.Contains(t, result, "-15.5%")

	// Regression (positive)
	result = formatDelta(10.2, true)
	assert.Contains(t, result, "+10.2%")
}
