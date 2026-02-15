package bench

import (
	"encoding/json"
	"fmt"
	"testing"
)

// fullMockRunner implements FullCommandRunner for testing
type fullMockRunner struct {
	responses   map[string]mockResponse
	runCalls    []string
	outputCalls []string
}

func newFullMockRunner() *fullMockRunner {
	return &fullMockRunner{
		responses: make(map[string]mockResponse),
	}
}

func (m *fullMockRunner) key(name string, args ...string) string {
	return fmt.Sprintf("%s %v", name, args)
}

func (m *fullMockRunner) SetResponse(name string, args []string, output []byte, err error) {
	m.responses[m.key(name, args...)] = mockResponse{output: output, err: err}
}

func (m *fullMockRunner) Run(name string, args ...string) error {
	m.runCalls = append(m.runCalls, m.key(name, args...))
	resp, ok := m.responses[m.key(name, args...)]
	if ok {
		return resp.err
	}
	return nil
}

func (m *fullMockRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.outputCalls = append(m.outputCalls, m.key(name, args...))
	resp, ok := m.responses[m.key(name, args...)]
	if ok {
		return resp.output, resp.err
	}
	return nil, nil
}

func TestStoreNotes(t *testing.T) {
	mock := newFullMockRunner()
	report := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo", NsPerOp: 1000}},
		},
	}

	err := StoreNotes(mock, report)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify git notes command was called
	if len(mock.runCalls) != 1 {
		t.Fatalf("expected 1 run call, got %d", len(mock.runCalls))
	}

	call := mock.runCalls[0]
	if call[:4] != "git " {
		t.Errorf("expected git command, got %q", call)
	}
}

func TestStoreNotesError(t *testing.T) {
	mock := newFullMockRunner()
	report := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{},
	}

	// Set up to fail
	data, _ := json.Marshal(report)
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "add", "-f", "-m", string(data), "HEAD"}, nil, fmt.Errorf("git error"))

	err := StoreNotes(mock, report)
	if err == nil {
		t.Error("expected error when git fails")
	}
}

func TestFetchPreviousNone(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, nil, fmt.Errorf("no notes"))

	report, sha, err := FetchPrevious(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if report != nil {
		t.Error("expected nil report when no previous")
	}
	if sha != "" {
		t.Error("expected empty sha when no previous")
	}
}

func TestFetchPreviousEmpty(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, []byte(""), nil)

	report, sha, err := FetchPrevious(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if report != nil {
		t.Error("expected nil report when empty sha")
	}
	if sha != "" {
		t.Error("expected empty sha")
	}
}

func TestFetchPreviousWithData(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, []byte("abc123\n"), nil)

	reportData := `{"packages":{"pkg":[{"name":"BenchmarkFoo","ns_per_op":1000}]}}`
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "show", "abc123"}, []byte(reportData), nil)

	report, sha, err := FetchPrevious(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if sha != "abc123" {
		t.Errorf("expected sha 'abc123', got %q", sha)
	}
	if !report.HasResults() {
		t.Error("expected report to have results")
	}
}

func TestFetchForCommitSuccess(t *testing.T) {
	mock := newFullMockRunner()
	reportData := `{"packages":{"pkg":[{"name":"BenchmarkFoo","ns_per_op":1000}]}}`
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "show", "abc123"}, []byte(reportData), nil)

	report, err := FetchForCommit(mock, "abc123")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}
}

func TestFetchForCommitNotFound(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "show", "abc123"}, nil, fmt.Errorf("no note found"))

	_, err := FetchForCommit(mock, "abc123")
	if err == nil {
		t.Error("expected error when no note found")
	}
}

func TestGetHeadSHA(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"rev-parse", "--short", "HEAD"}, []byte("abc1234\n"), nil)

	sha, err := GetHeadSHA(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if sha != "abc1234" {
		t.Errorf("expected 'abc1234', got %q", sha)
	}
}

func TestGetHeadSHAError(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"rev-parse", "--short", "HEAD"}, nil, fmt.Errorf("not a git repo"))

	_, err := GetHeadSHA(mock)
	if err == nil {
		t.Error("expected error when git fails")
	}
}

func TestParseNotesJSON(t *testing.T) {
	data := `{"packages":{"pkg":[{"name":"BenchmarkFoo","ns_per_op":1234.5}]}}`
	report, err := parseNotesJSON([]byte(data))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}

	results := report.Packages["pkg"]
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].NsPerOp != 1234.5 {
		t.Errorf("expected 1234.5 ns/op, got %f", results[0].NsPerOp)
	}
}

func TestParseNotesJSONInvalid(t *testing.T) {
	_, err := parseNotesJSON([]byte("not json"))
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}
