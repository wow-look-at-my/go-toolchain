package bench

import (
	"encoding/json"
	"fmt"
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	assert.Nil(t, err)

	// Verify git notes command was called
	require.Equal(t, 1, len(mock.runCalls))

	call := mock.runCalls[0]
	assert.Equal(t, "git ", call[:4])
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
	assert.NotNil(t, err)
}

func TestFetchPreviousNone(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, nil, fmt.Errorf("no notes"))

	report, sha, err := FetchPrevious(mock)
	assert.Nil(t, err)
	assert.Nil(t, report)
	assert.Equal(t, "", sha)
}

func TestFetchPreviousEmpty(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, []byte(""), nil)

	report, sha, err := FetchPrevious(mock)
	assert.Nil(t, err)
	assert.Nil(t, report)
	assert.Equal(t, "", sha)
}

func TestFetchPreviousWithData(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, []byte("abc123\n"), nil)

	reportData := `{"packages":{"pkg":[{"name":"BenchmarkFoo","ns_per_op":1000}]}}`
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "show", "abc123"}, []byte(reportData), nil)

	report, sha, err := FetchPrevious(mock)
	assert.Nil(t, err)
	require.NotNil(t, report)
	assert.Equal(t, "abc123", sha)
	assert.True(t, report.HasResults())
}

func TestFetchForCommitSuccess(t *testing.T) {
	mock := newFullMockRunner()
	reportData := `{"packages":{"pkg":[{"name":"BenchmarkFoo","ns_per_op":1000}]}}`
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "show", "abc123"}, []byte(reportData), nil)

	report, err := FetchForCommit(mock, "abc123")
	assert.Nil(t, err)
	require.NotNil(t, report)
}

func TestFetchForCommitNotFound(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "show", "abc123"}, nil, fmt.Errorf("no note found"))

	_, err := FetchForCommit(mock, "abc123")
	assert.NotNil(t, err)
}

func TestGetHeadSHA(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"rev-parse", "--short", "HEAD"}, []byte("abc1234\n"), nil)

	sha, err := GetHeadSHA(mock)
	assert.Nil(t, err)
	assert.Equal(t, "abc1234", sha)
}

func TestGetHeadSHAError(t *testing.T) {
	mock := newFullMockRunner()
	mock.SetResponse("git", []string{"rev-parse", "--short", "HEAD"}, nil, fmt.Errorf("not a git repo"))

	_, err := GetHeadSHA(mock)
	assert.NotNil(t, err)
}

func TestParseNotesJSON(t *testing.T) {
	data := `{"packages":{"pkg":[{"name":"BenchmarkFoo","ns_per_op":1234.5}]}}`
	report, err := parseNotesJSON([]byte(data))
	assert.Nil(t, err)
	require.NotNil(t, report)

	results := report.Packages["pkg"]
	require.Equal(t, 1, len(results))
	assert.Equal(t, 1234.5, results[0].NsPerOp)
}

func TestParseNotesJSONInvalid(t *testing.T) {
	_, err := parseNotesJSON([]byte("not json"))
	assert.NotNil(t, err)
}
