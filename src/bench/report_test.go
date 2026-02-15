package bench

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestParseBenchmarkOutput(t *testing.T) {
	input := `{"Time":"2024-01-01T00:00:00Z","Action":"start","Package":"pkg/foo"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"pkg/foo","Output":"BenchmarkFoo-8   \t   10000\t    123456 ns/op\t   1024 B/op\t      8 allocs/op\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"pkg/foo","Output":"BenchmarkBar-8   \t    5000\t    234567 ns/op\t   2048 B/op\t     16 allocs/op\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"pass","Package":"pkg/foo"}`

	report, err := ParseBenchmarkOutput([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Packages) != 1 {
		t.Fatalf("expected 1 package, got %d", len(report.Packages))
	}

	results, ok := report.Packages["pkg/foo"]
	if !ok {
		t.Fatal("expected pkg/foo in results")
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	foo := results[0]
	if foo.Name != "BenchmarkFoo-8" {
		t.Errorf("expected BenchmarkFoo-8, got %s", foo.Name)
	}
	if foo.Iterations != 10000 {
		t.Errorf("expected 10000 iterations, got %d", foo.Iterations)
	}
	if foo.NsPerOp != 123456 {
		t.Errorf("expected 123456 ns/op, got %f", foo.NsPerOp)
	}
	if foo.BytesPerOp != 1024 {
		t.Errorf("expected 1024 B/op, got %d", foo.BytesPerOp)
	}
	if foo.AllocsPerOp != 8 {
		t.Errorf("expected 8 allocs/op, got %d", foo.AllocsPerOp)
	}
}

func TestParseBenchmarkOutputNoAllocStats(t *testing.T) {
	input := `{"Action":"output","Package":"pkg","Output":"BenchmarkSimple-4   \t 1000000\t      1234 ns/op\n"}`

	report, err := ParseBenchmarkOutput([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	results := report.Packages["pkg"]
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	if results[0].BytesPerOp != 0 {
		t.Errorf("expected 0 B/op, got %d", results[0].BytesPerOp)
	}
	if results[0].AllocsPerOp != 0 {
		t.Errorf("expected 0 allocs/op, got %d", results[0].AllocsPerOp)
	}
}

func TestParseBenchmarkOutputEmpty(t *testing.T) {
	report, err := ParseBenchmarkOutput([]byte{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(report.Packages) != 0 {
		t.Errorf("expected empty packages, got %d", len(report.Packages))
	}

	if report.HasResults() {
		t.Error("expected HasResults() to be false")
	}
}

func TestParseBenchmarkOutputMalformedJSON(t *testing.T) {
	input := `not json at all
{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}
also not json`

	report, err := ParseBenchmarkOutput([]byte(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should still parse the valid line
	if len(report.Packages) != 1 {
		t.Errorf("expected 1 package, got %d", len(report.Packages))
	}
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
		if result != tc.expected {
			t.Errorf("formatBenchTime(%f) = %q, expected %q", tc.ns, result, tc.expected)
		}
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
		if result != tc.expected {
			t.Errorf("formatBenchBytes(%d) = %q, expected %q", tc.bytes, result, tc.expected)
		}
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

	if !strings.Contains(output, "pkg") {
		t.Error("expected output to contain package name")
	}
	if !strings.Contains(output, "Foo") {
		t.Error("expected output to contain benchmark name")
	}
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

	if !strings.Contains(output, "no benchmarks found") {
		t.Error("expected 'no benchmarks found' message")
	}
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
	if err := enc.Encode(report); err != nil {
		t.Fatalf("failed to encode: %v", err)
	}

	var decoded BenchmarkReport
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}

	if len(decoded.Packages["pkg"]) != 1 {
		t.Error("expected 1 result in decoded report")
	}
}

func TestHasResults(t *testing.T) {
	empty := &BenchmarkReport{Packages: map[string][]BenchmarkResult{}}
	if empty.HasResults() {
		t.Error("empty report should return false")
	}

	withResults := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo"}},
		},
	}
	if !withResults.HasResults() {
		t.Error("report with results should return true")
	}
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
	if !strings.Contains(output, "BenchmarkBar-8") {
		t.Error("expected BenchmarkBar-8 in output")
	}
	if !strings.Contains(output, "BenchmarkFoo-8") {
		t.Error("expected BenchmarkFoo-8 in output")
	}
	if !strings.Contains(output, "1234.56 ns/op") {
		t.Error("expected ns/op in output")
	}
	if !strings.Contains(output, "256 B/op") {
		t.Error("expected B/op in output")
	}
	if !strings.Contains(output, "4 allocs/op") {
		t.Error("expected allocs/op in output")
	}

	// Bar should come before Foo (sorted)
	barIdx := strings.Index(output, "BenchmarkBar")
	fooIdx := strings.Index(output, "BenchmarkFoo")
	if barIdx > fooIdx {
		t.Error("expected results sorted by name")
	}
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

	if strings.Contains(output, "B/op") {
		t.Error("should not include B/op when zero")
	}
	if strings.Contains(output, "allocs/op") {
		t.Error("should not include allocs/op when zero")
	}
}
