package cmd

import (
	"errors"
	"testing"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// runCodeQLAnalyze is a no-op when CODEQL_DIST is unset.
func TestRunCodeQLAnalyzeDisabled(t *testing.T) {
	t.Setenv("CODEQL_DIST", "")
	mock := runner.NewMock()
	runCodeQLAnalyze(mock)
	assert.Empty(t, mock.Calls(), "no commands should run when CodeQL is disabled")
}

// When enabled, runCodeQLAnalyze invokes finalize, analyze, and upload.
func TestRunCodeQLAnalyzeEnabled(t *testing.T) {
	t.Setenv("CODEQL_DIST", "/opt/codeql")
	t.Setenv("CODEQL_EXTRACTOR_GO_WIP_DATABASE", "/tmp/db")
	t.Setenv("GO_TOOLCHAIN_SKIP_SARIF_UPLOAD", "")
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_SHA", "deadbeef")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	InitTimeline()

	mock := runner.NewMock()
	runCodeQLAnalyze(mock)

	calls := mock.Calls()
	require.Len(t, calls, 3, "expected finalize, analyze, upload-results")
	assert.Equal(t, []string{"database", "finalize", "/tmp/db"}, calls[0].Args)
	assert.Equal(t, "analyze", calls[1].Args[1])
	assert.Equal(t, "upload-results", calls[2].Args[1])
}

// GO_TOOLCHAIN_SKIP_SARIF_UPLOAD suppresses the upload step (the
// surrounding action does the upload via github/codeql-action/upload-sarif).
func TestRunCodeQLAnalyzeSkipUpload(t *testing.T) {
	t.Setenv("CODEQL_DIST", "/opt/codeql")
	t.Setenv("CODEQL_EXTRACTOR_GO_WIP_DATABASE", "/tmp/db")
	t.Setenv("GO_TOOLCHAIN_SKIP_SARIF_UPLOAD", "1")
	InitTimeline()

	mock := runner.NewMock()
	runCodeQLAnalyze(mock)

	calls := mock.Calls()
	require.Len(t, calls, 2, "expected finalize + analyze, no upload")
	assert.Equal(t, []string{"database", "finalize", "/tmp/db"}, calls[0].Args)
	assert.Equal(t, "analyze", calls[1].Args[1])
}

// Analyze failure must be logged and swallowed (not propagate); upload skipped.
func TestRunCodeQLAnalyzeAnalyzeFails(t *testing.T) {
	t.Setenv("CODEQL_DIST", "/opt/codeql")
	t.Setenv("CODEQL_EXTRACTOR_GO_WIP_DATABASE", "/tmp/db")
	InitTimeline()

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		// fail the finalize to surface as analyze error
		return runner.MockProcess(nil, errors.New("boom")), nil
	}
	// Should not panic or propagate
	runCodeQLAnalyze(mock)

	// Only finalize was attempted (analyze step short-circuits on first error).
	calls := mock.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "database", calls[0].Args[0])
}
