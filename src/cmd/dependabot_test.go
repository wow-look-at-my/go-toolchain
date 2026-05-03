package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestBuildDepSnapshot_MissingSHA(t *testing.T) {
	t.Setenv("GITHUB_SHA", "")
	_, err := buildDepSnapshot()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "GITHUB_SHA")
}

func TestBuildDepSnapshot_NoGoMod(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	t.Setenv("GITHUB_SHA", "abc123")

	_, err := buildDepSnapshot()
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "go.mod")
}

func TestBuildDepSnapshot_Success(t *testing.T) {
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
	t.Setenv("GITHUB_SHA", "abc123")
	t.Setenv("GITHUB_REF", "")

	snapshot, err := buildDepSnapshot()
	require.Nil(t, err)
	assert.Equal(t, "refs/heads/main", snapshot.Ref)
}

func TestBuildDepSnapshot_IndirectDeps(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	gomod := "module test\ngo 1.21\n\nrequire (\n\tgithub.com/spf13/cobra v1.8.0\n\tgithub.com/spf13/pflag v1.0.5 // indirect\n)\n"
	os.WriteFile("go.mod", []byte(gomod), 0644)

	t.Setenv("GITHUB_SHA", "abc123")

	snapshot, err := buildDepSnapshot()
	require.Nil(t, err)

	manifest := snapshot.Manifests["go.mod"]
	assert.Equal(t, "direct", manifest.Resolved["github.com/spf13/cobra"].Relationship)
	assert.Equal(t, "indirect", manifest.Resolved["github.com/spf13/pflag"].Relationship)
}

func TestBuildDepSnapshot_WorkspaceRelativePath(t *testing.T) {
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
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	err := postDepSnapshot(&depSnapshot{})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "GITHUB_TOKEN")
}

func TestPostDepSnapshot_MissingRepo(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "test-token")
	t.Setenv("GITHUB_REPOSITORY", "")
	err := postDepSnapshot(&depSnapshot{})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "GITHUB_REPOSITORY")
}

func TestPostDepSnapshot_Success(t *testing.T) {
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
}

func TestMaybeSubmitDeps_NotCI(t *testing.T) {
	t.Setenv("CI", "")
	maybeSubmitDeps()
}

func TestMaybeSubmitDeps_NoRepo(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", "")
	maybeSubmitDeps()
}

func TestMaybeSubmitDeps_NoSHA(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REPOSITORY", "owner/repo")
	t.Setenv("GITHUB_SHA", "")
	maybeSubmitDeps()
}
