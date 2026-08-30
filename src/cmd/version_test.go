package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

func TestDirtyFilesExcludingToolchainWrites(t *testing.T) {
	// Guard files are ignored in every state, including migration deletions, while real changes remain.
	status := " M .gitignore\n" +
		" D gomemlimit_gen.go\n" +
		" D cmd/tool/gomemlimit_gen.go\n" +
		"?? gomemlimit_gen.go\n" +
		" M src/main.go\n"
	got := dirtyFilesExcludingToolchainWrites(status)
	assert.Equal(t, " M .gitignore\n M src/main.go", got)
}

func TestDirtyFilesExcludingToolchainWritesOnlyGuards(t *testing.T) {
	// A tree dirty *only* with guard files reads as clean.
	status := " D gomemlimit_gen.go\n?? cmd/tool/gomemlimit_gen.go\n"
	assert.Equal(t, "", dirtyFilesExcludingToolchainWrites(status))
}

func TestDirtyFilesExcludingToolchainWritesEmpty(t *testing.T) {
	assert.Equal(t, "", dirtyFilesExcludingToolchainWrites(""))
}

// The message tells the reader to review the diff, so a CI-only failure has to
// carry it: the runner's tree is gone by the time anyone reads the log.
func TestDirtyDiffPaths(t *testing.T) {
	status := " M go.mod\n?? build/extra.txt\nR  old.go -> new.go\n"
	assert.Equal(t, []string{"go.mod", "build/extra.txt", "new.go"}, dirtyDiffPaths(status))
	assert.Empty(t, dirtyDiffPaths(""))
}

// A dirty tree in CI is often the only place a change is visible, so the diff
// has to arrive or say why it did not. Returning nothing leaves the reader
// staring at a file list under an instruction to review something absent.
func TestDirtyDiffShowsTheChange(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		require.NoError(t, exec.Command("git", append([]string{"-C", dir}, args...)...).Run())
	}
	mod := filepath.Join(dir, "go.mod")
	require.NoError(t, os.WriteFile(mod, []byte("module example.com/x\n\ngo 1.27\n"), 0644))
	require.NoError(t, exec.Command("git", "-C", dir, "add", "go.mod").Run())
	require.NoError(t, exec.Command("git", "-C", dir, "commit", "-qm", "init").Run())
	require.NoError(t, os.WriteFile(mod, []byte("module example.com/x\n\ngo 1.28\n"), 0644))

	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(oldWd)

	got := dirtyDiff(" M go.mod")
	assert.Contains(t, got, "-go 1.27")
	assert.Contains(t, got, "+go 1.28")
}

// Every path out of dirtyDiff says something. Silence is what sent the last
// windows failure back around with nothing learned.
func TestDirtyDiffReportsWhenGitCannotAnswer(t *testing.T) {
	oldWd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(t.TempDir()))
	defer os.Chdir(oldWd)

	assert.Contains(t, dirtyDiff(" M go.mod"), "git diff failed")
	assert.Empty(t, dirtyDiff(""))
}

func TestStatusLineIsToolchainWrite(t *testing.T) {
	cases := map[string]bool{
		" D gomemlimit_gen.go":           true,
		"?? gomemlimit_gen.go":           true,
		" M cmd/tool/gomemlimit_gen.go":  true,
		"R  old.go -> gomemlimit_gen.go": true, // rename destination is the guard
		" M .gitignore":                  false,
		" M src/gomemlimit_gen.go.bak":   false,
		"":                               false,
	}
	for line, want := range cases {
		assert.Equalf(t, want, statusLineIsToolchainWrite(line, nil), "line %q", line)
	}
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

// This test redirects a REAL os.Stdout pipe, so it would trip the guard if
// version were not exempt from it (skipAgentGuard). Stubbing the agent check
// keeps that independent of the exemption: a change there must fail
// TestSkipCache_VersionSubcommandsSkip, not kill this whole test binary with
// the guard's own process exit.
func TestVersionRaw(t *testing.T) {
	oldCache := cachedVCS
	defer func() { cachedVCS = oldCache }()
	cachedVCS = &vcsInfo{Time: "2023-11-14T22:13:20Z"}

	origUnder := runningUnderAgentFn
	runningUnderAgentFn = func() (string, bool) { return "", false }
	t.Cleanup(func() { runningUnderAgentFn = origUnder })

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

func TestDiffOnlyDropsGuard(t *testing.T) {
	header := "diff --git a/.gitignore b/.gitignore\n" +
		"index abc1234..def5678 100644\n" +
		"--- a/.gitignore\n" +
		"+++ b/.gitignore\n" +
		"@@ -1,3 +1,2 @@\n"

	// Only the guard line removed -> the toolchain's own cleanup, excluded.
	assert.True(t, diffOnlyDropsGuard(header+" /build/\n-gomemlimit_gen.go\n vendor/\n"))

	// A real addition alongside the removal -> a developer edit, not excluded.
	assert.False(t, diffOnlyDropsGuard(header+"-gomemlimit_gen.go\n+something-new\n"))

	// Removing a non-guard line -> not excluded.
	assert.False(t, diffOnlyDropsGuard(header+" /build/\n-vendor/\n"))

	// No removal at all (empty diff, or pure additions) -> nothing to exclude.
	assert.False(t, diffOnlyDropsGuard(""))
	assert.False(t, diffOnlyDropsGuard(header+"+/build/\n"))
}
