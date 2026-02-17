package bench

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestStoreNotes(t *testing.T) {
	mock := runner.NewMock()
	report := &BenchmarkReport{
		Packages: map[string][]BenchmarkResult{
			"pkg": {{Name: "BenchmarkFoo", NsPerOp: 1000}},
		},
	}

	err := StoreNotes(mock, report)
	assert.Nil(t, err)

	// Verify git notes command was called
	calls := mock.Calls()
	require.Equal(t, 1, len(calls))
	assert.Equal(t, "git", calls[0].Name)
}

func TestStoreNotesError(t *testing.T) {
	mock := runner.NewMock()
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
	mock := runner.NewMock()
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, nil, fmt.Errorf("no notes"))

	report, sha, err := FetchPrevious(mock)
	assert.Nil(t, err)
	assert.Nil(t, report)
	assert.Equal(t, "", sha)
}

func TestFetchPreviousEmpty(t *testing.T) {
	mock := runner.NewMock()
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, []byte(""), nil)

	report, sha, err := FetchPrevious(mock)
	assert.Nil(t, err)
	assert.Nil(t, report)
	assert.Equal(t, "", sha)
}

func TestFetchPreviousWithData(t *testing.T) {
	mock := runner.NewMock()
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
	mock := runner.NewMock()
	reportData := `{"packages":{"pkg":[{"name":"BenchmarkFoo","ns_per_op":1000}]}}`
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "show", "abc123"}, []byte(reportData), nil)

	report, err := FetchForCommit(mock, "abc123")
	assert.Nil(t, err)
	require.NotNil(t, report)
}

func TestFetchForCommitNotFound(t *testing.T) {
	mock := runner.NewMock()
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "show", "abc123"}, nil, fmt.Errorf("no note found"))

	_, err := FetchForCommit(mock, "abc123")
	assert.NotNil(t, err)
}

func TestGetHeadSHA(t *testing.T) {
	mock := runner.NewMock()
	mock.SetResponse("git", []string{"rev-parse", "--short", "HEAD"}, []byte("abc1234\n"), nil)

	sha, err := GetHeadSHA(mock)
	assert.Nil(t, err)
	assert.Equal(t, "abc1234", sha)
}

func TestGetHeadSHAError(t *testing.T) {
	mock := runner.NewMock()
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
