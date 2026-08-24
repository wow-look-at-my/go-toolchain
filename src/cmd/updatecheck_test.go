package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withMockBuildhost points the update check at a test server and restores the
// originals when the returned func runs.
func withMockBuildhost(t *testing.T, server *httptest.Server) func() {
	t.Helper()
	oldBase := buildhostAPIBase
	oldClient := httpClient
	buildhostAPIBase = server.URL
	httpClient = server.Client()
	return func() {
		buildhostAPIBase = oldBase
		httpClient = oldClient
	}
}

// releaseServer serves rel at /releases/latest and 404s elsewhere.
func releaseServer(t *testing.T, rel buildhostRelease) *httptest.Server {
	t.Helper()
	return releaseServerWithList(t, rel, nil)
}

// releaseServerWithList serves both endpoints the check uses: the latest
// release, and the newest-first listing it identifies THIS binary from. A nil
// list answers 404, which is the "lookup failed" path.
func releaseServerWithList(t *testing.T, rel buildhostRelease, list []buildhostRelease) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			_ = json.NewEncoder(w).Encode(rel)
		case strings.HasSuffix(r.URL.Path, "/releases"):
			if list == nil {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(list)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestFetchLatestBuildhostRelease(t *testing.T) {
	pub := time.Date(2026, 6, 14, 5, 4, 25, 0, time.UTC)
	want := buildhostRelease{
		Version: "202", VersionNum: 202, GitCommit: "6d7723427895dc2e", GitBranch: "v1",
		Published: true, CreatedAt: pub.Add(-6 * time.Second), PublishedAt: &pub,
	}
	srv := releaseServer(t, want)
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	got, err := fetchLatestBuildhostRelease(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "202", got.Version)
	assert.Equal(t, "6d7723427895dc2e", got.GitCommit)
	assert.True(t, got.Published)
	require.NotNil(t, got.PublishedAt)
	assert.True(t, pub.Equal(*got.PublishedAt))
}

func TestFetchLatestBuildhostReleaseHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	_, err := fetchLatestBuildhostRelease(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

func TestComputeUpdateWarning_UpToDate(t *testing.T) {
	defer setVCS(t, "6d7723427895dc2eff7313e610fdb316a1bd5836", "2026-06-14T05:04:19Z")()

	pub := time.Date(2026, 6, 14, 5, 4, 25, 0, time.UTC) // published after our commit
	srv := releaseServer(t, buildhostRelease{
		Version: "202", GitCommit: "6d7723427895dc2eff7313e610fdb316a1bd5836",
		Published: true, PublishedAt: &pub,
	})
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	// Same commit -> up to date even though the release was published later.
	assert.Equal(t, "", computeUpdateWarning(context.Background()))
}

func TestComputeUpdateWarning_OutOfDate(t *testing.T) {
	defer setVCS(t, "0000000aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "2024-01-01T00:00:00Z")()

	pub := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC) // newer than our build
	srv := releaseServer(t, buildhostRelease{
		Version: "202", GitCommit: "ffffff222222222222222222222222222222ffff",
		Published: true, PublishedAt: &pub,
	})
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	// With no listing to identify this build, its commit stands in for the version it cannot know.
	msg := computeUpdateWarning(context.Background())
	assert.Contains(t, msg, "out of date")
	assert.Contains(t, msg, "0000000 < v202")
}

// TestComputeUpdateWarning_IsOneLineWithBothVersions pins the whole message:
// how far behind, mine, latest. Nothing else -- a reader deciding whether to
// update needs the two versions and the distance, and every extra word is one
// they have to skip past on every build.
func TestComputeUpdateWarning_IsOneLineWithBothVersions(t *testing.T) {
	const myCommit = "0000000aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	built := time.Date(2024, 5, 29, 0, 0, 0, 0, time.UTC)
	defer setVCS(t, myCommit, built.Format(time.RFC3339))()

	pub := built.Add(3 * 24 * time.Hour) // exactly three days newer
	latest := buildhostRelease{
		Version: "345", VersionNum: 345, GitCommit: "ffffff222222222222222222222222222222ffff",
		Published: true, PublishedAt: &pub,
	}
	mine := buildhostRelease{Version: "123", GitCommit: myCommit, Published: true, PublishedAt: &built}

	srv := releaseServerWithList(t, latest, []buildhostRelease{latest, mine})
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	msg := stripANSI(computeUpdateWarning(context.Background()))
	assert.Equal(t, "⇒ go-toolchain is 3 days out of date: v123 < v345", msg)
}

// stripANSI removes the color escapes so a test can assert the exact line a
// reader sees.
func stripANSI(s string) string {
	for _, code := range []string{colorYellow, colorReset} {
		s = strings.ReplaceAll(s, code, "")
	}
	return s
}

func TestComputeUpdateWarning_AheadOfPublished(t *testing.T) {
	// Our build is newer than the latest published release: stay quiet.
	defer setVCS(t, "aaaaaaaa1111111111111111111111111111aaaa", "2025-01-01T00:00:00Z")()

	pub := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC) // older than our build
	srv := releaseServer(t, buildhostRelease{
		Version: "100", GitCommit: "bbbbbbbb2222222222222222222222222222bbbb",
		Published: true, PublishedAt: &pub,
	})
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	assert.Equal(t, "", computeUpdateWarning(context.Background()))
}

func TestComputeUpdateWarning_DevBuild(t *testing.T) {
	defer setVCS(t, "", "")() // no revision/time -> resolvedCommit() == "unknown"
	// No server needed: it must return before any network call.
	assert.Equal(t, "", computeUpdateWarning(context.Background()))
}

func TestComputeUpdateWarning_FetchError(t *testing.T) {
	defer setVCS(t, "abc1234def", "2024-01-01T00:00:00Z")()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	// Any failure is silent.
	assert.Equal(t, "", computeUpdateWarning(context.Background()))
}

func TestComputeUpdateWarning_CreatedAtFallback(t *testing.T) {
	// No published_at -> fall back to created_at for the recency comparison.
	defer setVCS(t, "1111111aaaa", "2024-01-01T00:00:00Z")()

	created := time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC)
	srv := releaseServer(t, buildhostRelease{
		Version: "150", GitCommit: "2222222bbbb", Published: true, CreatedAt: created,
	})
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	assert.Contains(t, computeUpdateWarning(context.Background()), "out of date")
}

func TestCommitsMatch(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"6d7723427895dc2eff7313e610fdb316a1bd5836", "6d7723427895dc2eff7313e610fdb316a1bd5836", true},
		{"6d7723427895dc2eff7313e610fdb316a1bd5836", "6d77234", true}, // short prefix of long
		{"6D77234", "6d7723427895dc2e", true},                         // case-insensitive
		{"6d77234", "abcdef0", false},
		{"", "6d77234", false},
		{"6d77234", "", false},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, commitsMatch(c.a, c.b), "commitsMatch(%q,%q)", c.a, c.b)
	}
}

func TestStartUpdateCheckCannotBeDisabled(t *testing.T) {
	t.Cleanup(func() { activeUpdateCheck = nil })
	activeUpdateCheck = nil
	// There is no opt-out: even the old disable env var must not stop it.
	t.Setenv("GO_TOOLCHAIN_NO_UPDATE_CHECK", "1")

	srv := releaseServer(t, buildhostRelease{Version: "1", GitCommit: "abc", Published: true})
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	StartUpdateCheck()
	require.NotNil(t, activeUpdateCheck, "update check must always start")
	<-activeUpdateCheck.done // let the goroutine finish before the mock is restored
}

func TestReportUpdateCheckNoOp(t *testing.T) {
	t.Cleanup(func() { activeUpdateCheck = nil })
	activeUpdateCheck = nil
	// Safe no-op when nothing was started.
	ReportUpdateCheck()
}

func TestReportUpdateCheck_PrintsWhenReady(t *testing.T) {
	t.Cleanup(func() { activeUpdateCheck = nil })
	defer setVCS(t, "0000000aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "2024-01-01T00:00:00Z")()

	pub := time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
	srv := releaseServer(t, buildhostRelease{
		Version: "202", GitCommit: "ffffff222222222222222222222222222222ffff",
		Published: true, PublishedAt: &pub,
	})
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	StartUpdateCheck()
	require.NotNil(t, activeUpdateCheck)
	<-activeUpdateCheck.done // wait so Report takes the "ready" branch

	// logger.Warn routes to stdout as a ::warning annotation under GITHUB_ACTIONS=true; pin non-GHA mode for stderr capture.
	t.Setenv("GITHUB_ACTIONS", "")
	out := captureStderr(t, ReportUpdateCheck)
	assert.Contains(t, out, "out of date")
	assert.Contains(t, out, "v202")
}

func TestReportUpdateCheck_KillsWhenSlow(t *testing.T) {
	t.Cleanup(func() { activeUpdateCheck = nil })
	defer setVCS(t, "abc1234def", "2024-01-01T00:00:00Z")()

	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer withMockBuildhost(t, srv)()

	StartUpdateCheck()
	require.NotNil(t, activeUpdateCheck)

	// Report must return promptly (kill the in-flight check), never block on the slow server.
	returned := make(chan struct{})
	go func() { ReportUpdateCheck(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("ReportUpdateCheck blocked instead of killing the in-flight check")
	}

	close(release)           // unblock the server handler
	<-activeUpdateCheck.done // let the canceled goroutine finish before restore
}

// setVCS overrides the cached VCS info for a test and returns a restore func.
func setVCS(t *testing.T, revision, vcsTime string) func() {
	t.Helper()
	old := cachedVCS
	cachedVCS = &vcsInfo{Revision: revision, Time: vcsTime}
	return func() { cachedVCS = old }
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns what it
// wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	out, _ := io.ReadAll(r)
	return string(out)
}
