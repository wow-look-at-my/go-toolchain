package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "1 minute"},
		{2 * time.Minute, "2 minutes"},
		{59 * time.Minute, "59 minutes"},
		{1 * time.Hour, "1 hour"},
		{3 * time.Hour, "3 hours"},
		{23 * time.Hour, "23 hours"},
		{24 * time.Hour, "1 day"},
		{48 * time.Hour, "2 days"},
		{72 * time.Hour, "3 days"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		assert.Equal(t, tt.want, got)
	}
}

func TestCollectGitInfo(t *testing.T) {
	info := collectGitInfo()
	assert.NotEqual(t, "", info.commit)
	assert.NotEqual(t, "", info.timestamp)
	assert.NotEqual(t, "", info.version)
}

func TestCollectGitInfoFromEnv(t *testing.T) {
	// Set CI env vars
	t.Setenv("GITHUB_SHA", "env-sha-123456")
	t.Setenv("GITHUB_REF_TYPE", "tag")
	t.Setenv("GITHUB_REF_NAME", "v2.0.0")

	info := collectGitInfo()
	assert.Equal(t, "env-sha-123456", info.commit)
	assert.Equal(t, "v2.0.0", info.version)
}

func TestCollectGitInfoBranchRef(t *testing.T) {
	// Branch refs should NOT override version (only tags)
	t.Setenv("GITHUB_REF_TYPE", "branch")
	t.Setenv("GITHUB_REF_NAME", "main")

	info := collectGitInfo()
	// version should come from git describe, not the branch name
	assert.NotEqual(t, "main", info.version)
}

func TestEnvOr(t *testing.T) {
	t.Setenv("TEST_ENVOR_SET", "from-env")
	got := envOr("TEST_ENVOR_SET", "fallback")
	assert.Equal(t, string("from-env"), got)
	os.Unsetenv("TEST_ENVOR_UNSET")
	got := envOr("TEST_ENVOR_UNSET", "fallback")
	assert.Equal(t, string("fallback"), got)
}

func TestGithubRepoFromEnv(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "other-org/other-repo")
	// Re-initialize to pick up env var
	old := githubRepo
	githubRepo = envOr("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")
	defer func() { githubRepo = old }()

	assert.Equal(t, "other-org/other-repo", githubRepo)
}

func TestGitInfoLdflags(t *testing.T) {
	info := collectGitInfo()
	ldflags := info.ldflags()

	assert.NotEqual(t, "", ldflags)
	for _, want := range []string{"buildVersion", "buildCommit", "buildTimestamp", "buildDate"} {
		assert.Contains(t, ldflags, want)
	}
}

func TestGitInfoLdflagsReproducible(t *testing.T) {
	info := collectGitInfo()
	ldflags1 := info.ldflags()
	ldflags2 := info.ldflags()
	assert.Equal(t, ldflags2, ldflags1)
}

func TestGitInfoLdflagsSourceDateEpoch(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1700000000")
	info := gitInfo{version: "v1.0.0", commit: "abc123", timestamp: "1600000000"}
	ldflags := info.ldflags()
	// Should use SOURCE_DATE_EPOCH (1700000000) not git timestamp (1600000000)
	assert.Contains(t, ldflags, "2023-11-14")
}

func TestGitInfoLdflagsNoTimestamp(t *testing.T) {
	info := gitInfo{version: "v1.0.0", commit: "abc123"}
	ldflags := info.ldflags()
	assert.NotContains(t, ldflags, "buildDate")
}

func TestGitInfoString(t *testing.T) {
	tests := []struct {
		info gitInfo
		want string
	}{
		{gitInfo{version: "v1.0.0", commit: "abc1234567890"}, "v1.0.0"},
		{gitInfo{commit: "abc1234567890"}, "abc1234"},
		{gitInfo{commit: "abc"}, "abc"},
		{gitInfo{}, "unknown"},
	}
	for _, tt := range tests {
		got := tt.info.String()
	assert.Equal(t, tt.want, got)
	}
}

func TestPrintVersionInfo(t *testing.T) {
	old := buildVersion
	defer func() { buildVersion = old }()
	buildVersion = "test-version"
	printVersionInfo()
}

func TestPrintVersionInfoWithTimestamp(t *testing.T) {
	oldTs := buildTimestamp
	oldDate := buildDate
	defer func() {
		buildTimestamp = oldTs
		buildDate = oldDate
	}()
	buildTimestamp = "1700000000"
	buildDate = "2024-01-15T10:30:00Z"
	printVersionInfo()
}

func TestPrintStalenessDevBuild(t *testing.T) {
	old := buildTimestamp
	defer func() { buildTimestamp = old }()
	buildTimestamp = ""
	printStaleness()
}

func TestPrintStalenessUnknownCommit(t *testing.T) {
	oldTs := buildTimestamp
	oldCommit := buildCommit
	defer func() {
		buildTimestamp = oldTs
		buildCommit = oldCommit
	}()
	buildTimestamp = "1234567890"
	buildCommit = "unknown"
	printStaleness()
}

type githubCommitResponse struct {
	SHA    string `json:"sha"`
	Commit struct {
		Committer struct {
			Date time.Time `json:"date"`
		} `json:"committer"`
	} `json:"commit"`
}

func newGitHubMock(t *testing.T, commitTime time.Time, sha string, aheadBy int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/compare/") {
			json.NewEncoder(w).Encode(struct {
				AheadBy int `json:"ahead_by"`
			}{AheadBy: aheadBy})
			return
		}
		commit := githubCommitResponse{SHA: sha}
		commit.Commit.Committer.Date = commitTime
		json.NewEncoder(w).Encode([]githubCommitResponse{commit})
	}))
}

func withMockGitHub(t *testing.T, server *httptest.Server) func() {
	t.Helper()
	oldBase := githubAPIBase
	oldClient := httpClient
	setGithubAPIBase(server.URL)
	httpClient = server.Client()
	return func() {
		setGithubAPIBase(oldBase)
		httpClient = oldClient
	}
}

func TestFetchLatestCommitFromGitHub(t *testing.T) {
	commitTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	server := newGitHubMock(t, commitTime, "abc123def456", 0)
	defer server.Close()
	defer withMockGitHub(t, server)()

	info, err := fetchLatestCommitFromGitHub()
	require.Nil(t, err)
	assert.Equal(t, "abc123def456", info.sha)
	assert.Equal(t, commitTime.Unix(), info.timestamp)
}

func TestFetchLatestCommitFromGitHubHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	defer withMockGitHub(t, server)()

	_, err := fetchLatestCommitFromGitHub()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestFetchLatestCommitFromGitHubEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]githubCommitResponse{})
	}))
	defer server.Close()
	defer withMockGitHub(t, server)()

	_, err := fetchLatestCommitFromGitHub()
	assert.NotNil(t, err)
}

func TestFetchCommitsBehind(t *testing.T) {
	server := newGitHubMock(t, time.Now(), "head123", 7)
	defer server.Close()
	defer withMockGitHub(t, server)()

	count, err := fetchCommitsBehind("old123", "head123")
	require.Nil(t, err)
	assert.Equal(t, 7, count)
}

func TestFetchCommitsBehindHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer server.Close()
	defer withMockGitHub(t, server)()

	_, err := fetchCommitsBehind("old123", "head123")
	assert.NotNil(t, err)
}

func TestPrintStalenessUpToDate(t *testing.T) {
	oldTs := buildTimestamp
	oldCommit := buildCommit
	defer func() {
		buildTimestamp = oldTs
		buildCommit = oldCommit
	}()

	// Use a unix timestamp that's in the future relative to the mock
	buildTimestamp = "9999999999"
	buildCommit = "abc123"

	server := newGitHubMock(t, time.Now(), "abc123", 0)
	defer server.Close()
	defer withMockGitHub(t, server)()

	printStaleness()
}

func TestPrintStalenessBehind(t *testing.T) {
	oldTs := buildTimestamp
	oldCommit := buildCommit
	defer func() {
		buildTimestamp = oldTs
		buildCommit = oldCommit
	}()

	buildTimestamp = "1000000000" // old timestamp
	buildCommit = "old123"

	server := newGitHubMock(t, time.Now(), "new456", 5)
	defer server.Close()
	defer withMockGitHub(t, server)()

	printStaleness()
}

func TestPrintStalenessAPIFailure(t *testing.T) {
	oldTs := buildTimestamp
	oldCommit := buildCommit
	defer func() {
		buildTimestamp = oldTs
		buildCommit = oldCommit
	}()

	buildTimestamp = "1000000000"
	buildCommit = "abc123"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	defer withMockGitHub(t, server)()

	// Should print error message, not panic
	printStaleness()
}
