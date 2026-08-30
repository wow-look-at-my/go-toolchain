package codeql

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestEnabled(t *testing.T) {
	t.Setenv("CODEQL_DIST", "")
	assert.False(t, Enabled(), "Enabled() with CODEQL_DIST unset")

	t.Setenv("CODEQL_DIST", "/opt/codeql")
	assert.True(t, Enabled(), "Enabled() with CODEQL_DIST set")
}

func TestExtractInvokesGoExtractor(t *testing.T) {
	t.Setenv("CODEQL_EXTRACTOR_GO_ROOT", "/opt/codeql/go")
	mock := runner.NewMock()
	require.NoError(t, Extract(mock))

	calls := mock.Calls()
	require.Len(t, calls, 1)
	// filepath.Join builds the path, so the separator is the host's.
	assert.Contains(t, filepath.ToSlash(calls[0].Name), "/opt/codeql/go/tools/")
	assert.Contains(t, calls[0].Name, "go-extractor")
	assert.Equal(t, []string{"./..."}, calls[0].Args)
}

func TestExtractMissingEnv(t *testing.T) {
	t.Setenv("CODEQL_EXTRACTOR_GO_ROOT", "")
	require.Error(t, Extract(runner.NewMock()),
		"Extract should fail when CODEQL_EXTRACTOR_GO_ROOT unset")
}

func TestExtractPropagatesStderrOnFailure(t *testing.T) {
	t.Setenv("CODEQL_EXTRACTOR_GO_ROOT", "/opt/codeql/go")
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcessWithStderr(nil, []byte("permission denied"), errors.New("exit 1")), nil
	}
	err := Extract(mock)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "permission denied"),
		"error %q should contain stderr", err)
}

func TestAnalyzeRunsFinalizeAndAnalyze(t *testing.T) {
	t.Setenv("CODEQL_DIST", "/opt/codeql")
	t.Setenv("CODEQL_EXTRACTOR_GO_WIP_DATABASE", "/tmp/db")
	mock := runner.NewMock()
	sarif, err := Analyze(mock)
	require.NoError(t, err)
	assert.NotEmpty(t, sarif)

	calls := mock.Calls()
	require.Len(t, calls, 2)
	assert.Equal(t, []string{"database", "finalize", "/tmp/db"}, calls[0].Args)
	assert.Equal(t, "database", calls[1].Args[0])
	assert.Equal(t, "analyze", calls[1].Args[1])
}

func TestAnalyzeMissingDatabase(t *testing.T) {
	t.Setenv("CODEQL_EXTRACTOR_GO_WIP_DATABASE", "")
	_, err := Analyze(runner.NewMock())
	require.Error(t, err, "Analyze should fail when CODEQL_EXTRACTOR_GO_WIP_DATABASE unset")
}

func TestUploadSARIFRequiresEnv(t *testing.T) {
	cases := []struct {
		name                  string
		token, sha, ref, repo string
	}{
		{"no-token", "", "abc", "refs/heads/main", "o/r"},
		{"no-sha", "tok", "", "refs/heads/main", "o/r"},
		{"no-ref", "tok", "abc", "", "o/r"},
		{"no-repo", "tok", "abc", "refs/heads/main", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GITHUB_TOKEN", c.token)
			t.Setenv("GH_TOKEN", "")
			t.Setenv("GITHUB_SHA", c.sha)
			t.Setenv("GITHUB_REF", c.ref)
			t.Setenv("GITHUB_REPOSITORY", c.repo)
			t.Setenv("CODEQL_DIST", "/opt/codeql")
			require.Error(t, UploadSARIF(runner.NewMock(), "/tmp/r.sarif"))
		})
	}
}

func TestUploadSARIFPassesAllArgs(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_SHA", "deadbeef")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")
	t.Setenv("CODEQL_DIST", "/opt/codeql")

	mock := runner.NewMock()
	require.NoError(t, UploadSARIF(mock, "/tmp/r.sarif"))

	calls := mock.Calls()
	require.Len(t, calls, 1)
	assert.Equal(t, []string{
		"github", "upload-results",
		"--sarif=/tmp/r.sarif",
		"--commit=deadbeef",
		"--ref=refs/heads/main",
		"--repository=wow-look-at-my/go-toolchain",
	}, calls[0].Args)
}

func TestPlatformFor(t *testing.T) {
	cases := []struct {
		goos, plat, ext string
		wantErr         bool
	}{
		{"linux", "linux64", "", false},
		{"darwin", "osx64", "", false},
		{"windows", "win64", ".exe", false},
		{"plan9", "", "", true},
	}
	for _, c := range cases {
		t.Run(c.goos, func(t *testing.T) {
			plat, ext, err := platformFor(c.goos)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.plat, plat)
			assert.Equal(t, c.ext, ext)
		})
	}
}

func TestExtractorPathFor(t *testing.T) {
	p, err := extractorPathFor("/opt/codeql/go", "windows")
	require.NoError(t, err)
	assert.Equal(t, "/opt/codeql/go/tools/win64/go-extractor.exe", p)

	p, err = extractorPathFor("/opt/codeql/go", "darwin")
	require.NoError(t, err)
	assert.Equal(t, "/opt/codeql/go/tools/osx64/go-extractor", p)

	_, err = extractorPathFor("/opt/codeql/go", "freebsd")
	require.Error(t, err)
}

func TestCodeqlBinFor(t *testing.T) {
	assert.Equal(t, "/opt/codeql/codeql", codeqlBinFor("/opt/codeql", "linux"))
	assert.Equal(t, "/opt/codeql/codeql", codeqlBinFor("/opt/codeql", "darwin"))
	assert.Equal(t, "/opt/codeql/codeql.exe", codeqlBinFor("/opt/codeql", "windows"))
}

func TestUploadSARIFFallsBackToGHToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "ghtok")
	t.Setenv("GITHUB_SHA", "deadbeef")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("CODEQL_DIST", "/opt/codeql")

	mock := runner.NewMock()
	require.NoError(t, UploadSARIF(mock, "/tmp/r.sarif"))

	calls := mock.Calls()
	require.Len(t, calls, 1)
	require.NotNil(t, calls[0].Env)
	got, _ := calls[0].Env.Get("GITHUB_TOKEN")
	assert.Equal(t, "ghtok", got)
}
