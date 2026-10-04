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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// The stand-in writes its arguments to a file, which is what each test below
// reads.
func writeForkBranchScript(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, forkSubmoduleDir, filepath.FromSlash(forkBranchScript))
	require.NoError(t, os.MkdirAll(filepath.Dir(script), 0o755))
	record := filepath.Join(dir, "said")
	body := "#!/usr/bin/env bash\nprintf '%s' \"$*\" > " + record + "\n"
	require.NoError(t, os.WriteFile(script, []byte(body), 0o755))
	t.Chdir(dir)
	return record
}

// fakeRunLockStore serves the buildhost run lock API and an OIDC token endpoint.
func fakeRunLockStore(t *testing.T, locked map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/token":
			assert.Equal(t, "Bearer request-token", r.Header.Get("Authorization"))
			json.NewEncoder(w).Encode(map[string]string{"value": "oidc"})
		case r.URL.Path == "/api/v1/run-locks" && r.Method == http.MethodGet:
			assert.Equal(t, "Bearer oidc", r.Header.Get("Authorization"))
			assert.Equal(t, "2", r.URL.Query().Get("run_attempt"))
			v, ok := locked[r.URL.Query().Get("name")]
			json.NewEncoder(w).Encode(runLockBody{Value: v, Found: ok})
		case r.URL.Path == "/api/v1/run-locks" && r.Method == http.MethodPost:
			var b runLockBody
			require.NoError(t, json.NewDecoder(r.Body).Decode(&b))
			if _, ok := locked[b.Name]; !ok {
				locked[b.Name] = b.Value
			}
			json.NewEncoder(w).Encode(runLockBody{Value: locked[b.Name]})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")
	t.Setenv("GITHUB_RUN_ID", "7")
	t.Setenv("GITHUB_RUN_ATTEMPT", "2")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", srv.URL+"/token?api-version=2")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")
	t.Setenv(runLockStoreEnv, srv.URL)
	return srv
}

// The first job of a run attempt locks the branch head it sees in buildhost.
func TestLockedRunValueClaimsTheHeadWhenNothingIsLocked(t *testing.T) {
	t.Serial()
	locked := map[string]string{}
	fakeRunLockStore(t, locked)

	got, err := lockedRunValue("github.com/wow-look-at-my/gosmopolitan@master", "aaa")
	require.NoError(t, err)
	assert.Equal(t, "aaa", got)
	assert.Equal(t, "aaa", locked["github.com/wow-look-at-my/gosmopolitan@master"])
}

// A later job of the same attempt builds what the first one locked, whatever the head is now.
func TestLockedRunValueKeepsWhatTheRunLocked(t *testing.T) {
	t.Serial()
	fakeRunLockStore(t, map[string]string{"github.com/wow-look-at-my/gosmopolitan@master": "aaa"})

	got, err := lockedRunValue("github.com/wow-look-at-my/gosmopolitan@master", "bbb")
	require.NoError(t, err)
	assert.Equal(t, "aaa", got)
}

func TestBranchForkSubmodulesNamesTheBranch(t *testing.T) {
	record := writeForkBranchScript(t)
	for _, args := range [][]string{
		{"init", "--initial-branch", "claude/pin"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "test"},
		{"commit", "--allow-empty", "-m", "first"},
	} {
		require.NoError(t, exec.Command("git", args...).Run())
	}

	require.NoError(t, branchForkSubmodules(runner.New()))

	said, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.Equal(t, "claude/pin", string(said))
}

func TestBranchForkSubmodulesOutsideARepository(t *testing.T) {
	record := writeForkBranchScript(t)

	require.NoError(t, branchForkSubmodules(runner.New()))

	said, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.Empty(t, string(said))
}

func TestBranchForkSubmodulesWithoutTheScript(t *testing.T) {
	t.Chdir(t.TempDir())

	err := branchForkSubmodules(runner.New())

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), forkBranchScript), err.Error())
}
