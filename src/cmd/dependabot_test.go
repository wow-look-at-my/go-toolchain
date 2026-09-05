package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildDepSnapshot_MissingSHA(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_SHA", "")
	_, err := buildDepSnapshot()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "GITHUB_SHA")
}

func TestBuildDepSnapshot_NoGoMod(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	t.Setenv("GITHUB_SHA", "abc123")

	_, err := buildDepSnapshot()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "go.mod")
}

func TestBuildDepSnapshot_Success(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_SHA", "abc123def456")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("GITHUB_RUN_ID", "99999")
	t.Setenv("GITHUB_WORKSPACE", "")

	snapshot, err := buildDepSnapshot()
	require.Nil(t, err)

	assert.Equal(t, 0, snapshot.Version)
	assert.Equal(t, "abc123def456", snapshot.SHA)
	assert.Equal(t, "refs/heads/main", snapshot.Ref)
	assert.Equal(t, "99999", snapshot.Job.ID)
	assert.Equal(t, "go-toolchain", snapshot.Job.Correlator)
	assert.Equal(t, "go-toolchain", snapshot.Detector.Name)
	assert.NotEmpty(t, snapshot.Scanned)
	assert.NotEqual(t, 0, len(snapshot.Manifests))

	for _, manifest := range snapshot.Manifests {
		assert.NotEqual(t, 0, len(manifest.Resolved))

		cobra, ok := manifest.Resolved["github.com/spf13/cobra"]
		assert.True(t, ok)
		assert.Equal(t, "direct", cobra.Relationship)
		assert.Equal(t, "runtime", cobra.Scope)
		assert.Equal(t, "pkg:golang/github.com/spf13/cobra@v1.10.2", cobra.PackageURL)
		break
	}
}

func TestBuildDepSnapshot_DefaultRef(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_REF", "")

	snapshot, err := buildDepSnapshot()
	require.Nil(t, err)
	assert.Equal(t, "refs/heads/main", snapshot.Ref)
}

func TestBuildDepSnapshot_IndirectDeps(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	gomod := "module test\ngo 1.21\n\nrequire (\n\tgithub.com/spf13/cobra v1.8.0\n\tgithub.com/spf13/pflag v1.0.5 // indirect\n)\n"
	os.WriteFile("go.mod", []byte(gomod), 0644)

	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_WORKSPACE", "")

	snapshot, err := buildDepSnapshot()
	require.Nil(t, err)

	manifest := snapshot.Manifests["go.mod"]
	assert.Equal(t, "direct", manifest.Resolved["github.com/spf13/cobra"].Relationship)
	assert.Equal(t, "indirect", manifest.Resolved["github.com/spf13/pflag"].Relationship)
}

func TestBuildDepSnapshot_WorkspaceRelativePath(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_WORKSPACE", "/")

	snapshot, err := buildDepSnapshot()
	require.Nil(t, err)

	for loc := range snapshot.Manifests {
		assert.NotEmpty(t, loc)
		assert.NotEqual(t, "/", loc[:1])
	}
}

func TestPostDepSnapshot_MissingToken(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	err := postDepSnapshot(&depSnapshot{})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "GITHUB_TOKEN")
	// The error must tell the user how to wire the token up.
	assert.Contains(t, err.Error(), "github.token")
}

func TestPostDepSnapshot_MissingRepo(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GITHUB_REPOSITORY", "")
	err := postDepSnapshot(&depSnapshot{})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "GITHUB_REPOSITORY")
}

func TestPostDepSnapshot_Success(t *testing.T) {
	t.Serial()
	var received depSnapshot
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.URL.Path, "/dependency-graph/snapshots")
		assert.Equal(t, "token test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))

		json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	setGithubAPIBase(srv.URL)
	defer setGithubAPIBase(oldBase)

	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")

	snapshot := &depSnapshot{
		Version: 0,
		SHA:     "abc123",
		Ref:     "refs/heads/main",
		Manifests: map[string]depManifest{
			"go.mod": {
				Name: "go.mod",
				File: depManifestFile{SourceLocation: "go.mod"},
				Resolved: map[string]depResolved{
					"example.com/foo": {
						PackageURL:   "pkg:golang/example.com/foo@v1.0.0",
						Relationship: "direct",
						Scope:        "runtime",
					},
				},
			},
		},
	}

	err := postDepSnapshot(snapshot)
	require.Nil(t, err)

	assert.Equal(t, "abc123", received.SHA)
	assert.Equal(t, 1, len(received.Manifests["go.mod"].Resolved))
}

func TestPostDepSnapshot_GHTokenFallback(t *testing.T) {
	t.Serial()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "token fallback-token", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	setGithubAPIBase(srv.URL)
	defer setGithubAPIBase(oldBase)

	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "fallback-token")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")

	err := postDepSnapshot(&depSnapshot{Manifests: map[string]depManifest{}})
	require.Nil(t, err)
}

func TestPostDepSnapshot_APIError(t *testing.T) {
	t.Serial()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"Resource not accessible"}`))
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	setGithubAPIBase(srv.URL)
	defer setGithubAPIBase(oldBase)

	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")

	err := postDepSnapshot(&depSnapshot{Manifests: map[string]depManifest{}})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
	assert.Contains(t, err.Error(), "Resource not accessible")
	// A forbidden reply means the token lacks a permission; the error must name it and how to grant it.
	assert.Contains(t, err.Error(), "contents: write")
}

func TestPostDepSnapshot_APIErrorNon403(t *testing.T) {
	t.Serial()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"message":"boom"}`))
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	setGithubAPIBase(srv.URL)
	defer setGithubAPIBase(oldBase)

	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")

	err := postDepSnapshot(&depSnapshot{Manifests: map[string]depManifest{}})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
	assert.Contains(t, err.Error(), "boom")
	// The permissions guidance belongs to the forbidden reply alone.
	assert.NotContains(t, err.Error(), "contents: write")
}

func TestMaybeSubmitDeps_NotCI(t *testing.T) {
	t.Serial()
	t.Setenv("CI", "")
	assert.Nil(t, maybeSubmitDeps())
}

func TestMaybeSubmitDeps_NoRepo(t *testing.T) {
	t.Serial()
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", "")
	assert.Nil(t, maybeSubmitDeps())
}

func TestMaybeSubmitDeps_NoSHA(t *testing.T) {
	t.Serial()
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")
	t.Setenv("GITHUB_SHA", "")
	assert.Nil(t, maybeSubmitDeps())
}

func TestMaybeSubmitDeps_Success(t *testing.T) {
	t.Serial()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	setGithubAPIBase(srv.URL)
	defer setGithubAPIBase(oldBase)

	cwd, err := os.Getwd()
	require.Nil(t, err)
	t.Setenv("GITHUB_WORKSPACE", cwd)
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_TOKEN", "test-token")

	require.Nil(t, maybeSubmitDeps())
}

func TestMaybeSubmitDeps_SubmissionFailureFatal(t *testing.T) {
	t.Serial()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	setGithubAPIBase(srv.URL)
	defer setGithubAPIBase(oldBase)

	cwd, err := os.Getwd()
	require.Nil(t, err)
	t.Setenv("GITHUB_WORKSPACE", cwd)
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_TOKEN", "test-token")

	err = maybeSubmitDeps()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
	assert.Contains(t, err.Error(), "contents: write")
}

func TestMaybeSubmitDeps_SnapshotFailureFatal(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	t.Setenv("GITHUB_WORKSPACE", dir)
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")
	t.Setenv("GITHUB_SHA", "abc123")

	err := maybeSubmitDeps()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "dependency snapshot failed")
}

// This repo's own smoke jobs drive the full pipeline inside a throwaway module
// under RUNNER_TEMP; submitting there would publish the fixture's dependencies
// as this repository's dependency graph. That carve-out exists for this
// repository alone -- see TestMaybeSubmitDeps_OtherRepoCannotSkipByBuildingElsewhere.
func TestMaybeSubmitDeps_SkipsSmokeFixtureInOwnRepo(t *testing.T) {
	t.Serial()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	setGithubAPIBase(srv.URL)
	defer setGithubAPIBase(oldBase)

	// Workspace and build dir are siblings, matching GITHUB_WORKSPACE vs RUNNER_TEMP.
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	elsewhere := filepath.Join(root, "smokemod")
	require.Nil(t, os.MkdirAll(workspace, 0o755))
	require.Nil(t, os.MkdirAll(elsewhere, 0o755))

	t.Chdir(elsewhere)

	t.Setenv("GITHUB_WORKSPACE", workspace)
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", selfRepository)
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_TOKEN", "test-token")

	require.Nil(t, maybeSubmitDeps())
	assert.Equal(t, 0, requests, "the smoke fixture must not be submitted as this repository's dependency graph")
}

// The load-bearing case. "Build somewhere other than the checkout" must not
// become the opt-out that GO_TOOLCHAIN_NO_DEP_SUBMISSION was: for every
// repository but this repository it is a hard failure, never a quiet skip. Without
// this, any repo could dodge dependency submission by cd-ing to a temp dir and
// stay green while dropping out of vulnerability scanning.
func TestMaybeSubmitDeps_OtherRepoCannotSkipByBuildingElsewhere(t *testing.T) {
	t.Serial()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	setGithubAPIBase(srv.URL)
	defer setGithubAPIBase(oldBase)

	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	elsewhere := filepath.Join(root, "smokemod")
	require.Nil(t, os.MkdirAll(workspace, 0o755))
	require.Nil(t, os.MkdirAll(elsewhere, 0o755))

	t.Chdir(elsewhere)

	t.Setenv("GITHUB_WORKSPACE", workspace)
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", "someone/else")
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_TOKEN", "test-token")

	err := maybeSubmitDeps()
	require.NotNil(t, err, "building outside the checkout must fail, not silently skip")
	assert.Contains(t, err.Error(), "refusing to submit")
	assert.Contains(t, err.Error(), "not a supported way to skip submission")
	assert.Equal(t, 0, requests, "the fixture's dependencies must never be posted as another repository's graph")
}

// The guard must not swing the other way: a real build, in the checkout, submits.
func TestMaybeSubmitDeps_SubmitsRepoWorkspace(t *testing.T) {
	t.Serial()
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	oldBase := githubAPIBase
	setGithubAPIBase(srv.URL)
	defer setGithubAPIBase(oldBase)

	workspace := t.TempDir()
	require.Nil(t, os.WriteFile(filepath.Join(workspace, "go.mod"),
		[]byte("module example.com/inrepo\n\ngo 1.25\n"), 0o644))

	t.Chdir(workspace)

	t.Setenv("GITHUB_WORKSPACE", workspace)
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_TOKEN", "test-token")

	require.Nil(t, maybeSubmitDeps())
	assert.Equal(t, 1, requests, "a build in the repository's own checkout must submit")
}
