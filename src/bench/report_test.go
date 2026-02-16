package bench

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

)

func TestParseBenchmarkOutput(t *testing.T) {
	input := `{"Time":"2024-01-01T00:00:00Z","Action":"start","Package":"pkg/foo"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"pkg/foo","Output":"BenchmarkFoo-8   \t   10000\t    123456 ns/op\t   1024 B/op\t      8 allocs/op\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"pkg/foo","Output":"BenchmarkBar-8   \t    5000\t    234567 ns/op\t   2048 B/op\t     16 allocs/op\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"pass","Package":"pkg/foo"}`

	report, err := ParseBenchmarkOutput([]byte(input))
	require.Nil(t, err)

	require.Equal(t, 1, len(report.Packages))

	results, ok := report.Packages["pkg/foo"]
	require.True(t, ok)

	require.Equal(t, 2, len(results))

	foo := results[0]
	assert.Equal(t, "BenchmarkFoo-8", foo.Name)
	assert.Equal(t, 10000, foo.Iterations)
	assert.Equal(t, 123456, foo.NsPerOp)
	assert.Equal(t, 1024, foo.BytesPerOp)
	assert.Equal(t, 8, foo.AllocsPerOp)
}

func TestParseBenchmarkOutputNoAllocStats(t *testing.T) {
	input := `{"Action":"output","Package":"pkg","Output":"BenchmarkSimple-4   \t 1000000\t      1234 ns/op\n"}`

	report, err := ParseBenchmarkOutput([]byte(input))
	require.Nil(t, err)

	results := report.Packages["pkg"]
	require.Equal(t, 1, len(results))

	assert.Equal(t, 0, results[0].BytesPerOp)
	assert.Equal(t, 0, results[0].AllocsPerOp)
}

func TestParseBenchmarkOutputEmpty(t *testing.T) {
	report, err := ParseBenchmarkOutput([]byte{})
	require.Nil(t, err)

	assert.Equal(t, 0, len(report.Packages))

	assert.False(t, report.HasResults())
}

func TestParseBenchmarkOutputMalformedJSON(t *testing.T) {
	input := `not json at all
{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}
also not json`

	report, err := ParseBenchmarkOutput([]byte(input))
	require.Nil(t, err)

	// Should still parse the valid line
	assert.Equal(t, 1, len(report.Packages))
}

func TestFormatBenchTime(t *testing.T) {
	tests := []struct {
		ns       float64
		expected string
	}{
		{1.5, "1.5 ns"},
		{1500, "1.50 µs"},
		{1500000, "1.50 ms"},
		{1500000000, "1.50 s"},
	}

	for _, tc := range tests {
		result := formatBenchTime(tc.ns)
		assert.Equal(t, tc.expected, result)
	}
}

func TestFormatBenchBytes(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{512, "512 B"},
		{1536, "1.5 KB"},
		{1572864, "1.5 MB"},
	}

	for _, tc := range tests {
		result := formatBenchBytes(tc.bytes)
		assert.Equal(t, tc.expected, result)
	}
}

func TestBenchmarkReportPrint(t *testing.T) {
	report := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"example.com/pkg": {
				{Name: "BenchmarkFoo-8", Package: "example.com/pkg", Iterations: 1000, NsPerOp: 1234, BytesPerOp: 256, AllocsPerOp: 4},
			},
		},
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	report.Print()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, "pkg")
	assert.Contains(t, output, "Foo")
}

func TestBenchmarkReportPrintEmpty(t *testing.T) {
	report := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{},
	}

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	report.Print()

	w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	assert.Contains(t, output, "no benchmarks found")
}

func TestBenchmarkReportJSON(t *testing.T) {
	report := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {
				{Name: "BenchmarkFoo", Iterations: 1000, NsPerOp: 1234.5, BytesPerOp: 256, AllocsPerOp: 4},
			},
		},
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	require.NoError(t, enc.Encode(report))

	var decoded BenchmarkReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))

	assert.Equal(t, 1, len(decoded.Packages["pkg"]))
}

func TestHasResults(t *testing.T) {
	empty := &BenchmarkReport{Packages: map[string][]BenchmarkResult{}}
	assert.False(t, empty.HasResults())

	withResults := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo"}},
		},
	}
	assert.True(t, withResults.HasResults())
}

func TestToBenchstat(t *testing.T) {
	report := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg/foo": {
				{Name: "BenchmarkBar-8", Iterations: 5000, NsPerOp: 2345.67, BytesPerOp: 512, AllocsPerOp: 8},
				{Name: "BenchmarkFoo-8", Iterations: 10000, NsPerOp: 1234.56, BytesPerOp: 256, AllocsPerOp: 4},
			},
		},
	}

	output := report.ToBenchstat()

	// Should be sorted by name
	assert.Contains(t, output, "BenchmarkBar-8")
	assert.Contains(t, output, "BenchmarkFoo-8")
	assert.Contains(t, output, "1234.56 ns/op")
	assert.Contains(t, output, "256 B/op")
	assert.Contains(t, output, "4 allocs/op")

	// Bar should come before Foo (sorted)
	barIdx := strings.Index(output, "BenchmarkBar")
	fooIdx := strings.Index(output, "BenchmarkFoo")
	assert.LessOrEqual(t, barIdx, fooIdx)
}

func TestToBenchstatNoAllocs(t *testing.T) {
	report := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {
				{Name: "BenchmarkSimple-4", Iterations: 1000, NsPerOp: 500.0, BytesPerOp: 0, AllocsPerOp: 0},
			},
		},
	}

	output := report.ToBenchstat()

	assert.NotContains(t, output, "B/op")
	assert.NotContains(t, output, "allocs/op")
}
