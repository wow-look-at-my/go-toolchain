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

func TestCheckDirtyInCISkipsOutsideCI(t *testing.T) {
	t.Setenv("CI", "")
	assert.NoError(t, checkDirtyInCI())
}

func TestResolvedVersionFromVCS(t *testing.T) {
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	cachedVCS = &vcsInfo{Time: "2023-11-14T22:13:20Z"}
	assert.Equal(t, "v0.0.1700000000", resolvedVersion())
}

func TestResolvedVersionNoVCS(t *testing.T) {
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	cachedVCS = &vcsInfo{}
	assert.Equal(t, "dev", resolvedVersion())
}

func TestEnvOr(t *testing.T) {
	t.Setenv("TEST_ENVOR_SET", "from-env")
	got := envOr("TEST_ENVOR_SET", "fallback")
	assert.Equal(t, "from-env", got)
	os.Unsetenv("TEST_ENVOR_UNSET")
	got = envOr("TEST_ENVOR_UNSET", "fallback")
	assert.Equal(t, "fallback", got)
}

func TestGithubRepoFromEnv(t *testing.T) {
	t.Setenv("GITHUB_REPOSITORY", "other-org/other-repo")
	// Re-initialize to pick up env var
	old := githubRepo
	githubRepo = envOr("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")
	defer func() { githubRepo = old }()

	assert.Equal(t, "other-org/other-repo", githubRepo)
}

func TestVersionRaw(t *testing.T) {
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	cachedVCS = &vcsInfo{Time: "2023-11-14T22:13:20Z"}

	cmd := rootCmd
	buf := new(strings.Builder)
	cmd.SetOut(buf)

	// Invoke via cobra directly
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	rootCmd.SetArgs([]string{"version", "raw"})
	rootCmd.Execute() //nolint:errcheck
	w.Close()
	os.Stdout = oldStdout

	var out strings.Builder
	buf2 := make([]byte, 1024)
	n, _ := r.Read(buf2)
	out.Write(buf2[:n])
	assert.Contains(t, out.String(), "v0.0.1700000000")
}

func TestRunVersionJSON_DevBuild(t *testing.T) {
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	cachedVCS = &vcsInfo{}

	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w
	runVersionJSON(nil, nil)
	w.Close()
	os.Stdout = oldStdout

	var buf strings.Builder
	tmp := make([]byte, 4096)
	n, _ := r.Read(tmp)
	buf.Write(tmp[:n])

	var out versionOutput
	require.Nil(t, json.Unmarshal([]byte(buf.String()), &out))
	assert.Equal(t, "dev", out.Version)
	assert.Equal(t, "unknown", out.Commit)
	assert.Equal(t, "", out.CommitDate)
	assert.Nil(t, out.CommitsBehind)
}

func TestRunVersionJSON_WithVCS(t *testing.T) {
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	cachedVCS = &vcsInfo{
		Revision: "abc123",
		Time:     "2023-11-14T22:13:20Z",
	}

	server := newGitHubMock(t, time.Unix(1700000000, 0), "abc123", 0)
	defer server.Close()
	defer withMockGitHub(t, server)()

	r, w, _ := os.Pipe()
	oldStdout := os.Stdout
	os.Stdout = w
	runVersionJSON(nil, nil)
	w.Close()
	os.Stdout = oldStdout

	var buf strings.Builder
	tmp := make([]byte, 4096)
	n, _ := r.Read(tmp)
	buf.Write(tmp[:n])

	var out versionOutput
	require.Nil(t, json.Unmarshal([]byte(buf.String()), &out))
	assert.Equal(t, "v0.0.1700000000", out.Version)
	assert.Equal(t, "abc123", out.Commit)
	assert.Equal(t, "2023-11-14T22:13:20Z", out.CommitDate)
	require.NotNil(t, out.CommitsBehind)
	assert.Equal(t, 0, *out.CommitsBehind)
}

func TestPrintVersionInfo(t *testing.T) {
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	cachedVCS = &vcsInfo{
		Revision: "abc123",
		Time:     "2023-11-14T22:13:20Z",
	}
	printVersionInfo()
}

func TestPrintStalenessDevBuild(t *testing.T) {
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	cachedVCS = &vcsInfo{}
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
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	// Use a timestamp that's in the future relative to the mock
	cachedVCS = &vcsInfo{
		Revision: "abc123",
		Time:     "2300-01-01T00:00:00Z",
	}

	server := newGitHubMock(t, time.Now(), "abc123", 0)
	defer server.Close()
	defer withMockGitHub(t, server)()

	printStaleness()
}

func TestPrintStalenessBehind(t *testing.T) {
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	cachedVCS = &vcsInfo{
		Revision: "old123",
		Time:     "2001-09-09T01:46:40Z",
	}

	server := newGitHubMock(t, time.Now(), "new456", 5)
	defer server.Close()
	defer withMockGitHub(t, server)()

	printStaleness()
}

func TestPrintStalenessAPIFailure(t *testing.T) {
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	cachedVCS = &vcsInfo{
		Revision: "abc123",
		Time:     "2001-09-09T01:46:40Z",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	defer withMockGitHub(t, server)()

	// Should print error message, not panic
	printStaleness()
}
