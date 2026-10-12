package cmd

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// freshReleaseEnv clears the per-process answers so a test resolves again,
// and restores the fork's variables when it ends.
func freshReleaseEnv(t *testing.T) {
	t.Helper()
	t.Setenv(gosmoReleaseEnv, "")
	t.Setenv(gosmoReleaseRevisionEnv, "")
	t.Setenv("GITHUB_OUTPUT", "")
	reset := func() {
		releaseOnce, releaseValue, releaseErr = sync.Once{}, "", nil
		releaseEnvOnce, releaseEnvErr = sync.Once{}, nil
	}
	reset()
	t.Cleanup(reset)
}

func TestEnsureReleaseEnvHandsTheForkTheNumberAndCommit(t *testing.T) {
	t.Serial()
	freshReleaseEnv(t)
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv(ReleaseEnv, "412")
	t.Setenv(RevisionEnv, "660ecca3f00d8e1b2c3d4e5f60718293a4b5c6d7")
	require.NoError(t, ensureReleaseEnv())
	assert.Equal(t, "412", os.Getenv(gosmoReleaseEnv))
	assert.Equal(t, "660ecca3f00d8e1b2c3d4e5f60718293a4b5c6d7", os.Getenv(gosmoReleaseRevisionEnv))
}

func TestEnsureReleaseEnvFailsTheBuildWhenCICannotGetANumber(t *testing.T) {
	t.Serial()
	freshReleaseEnv(t)
	ciEnv(t, "http://127.0.0.1:1")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	require.ErrorContains(t, ensureReleaseEnv(), "id-token: write")
	assert.Equal(t, "", os.Getenv(gosmoReleaseEnv))
}

func TestEnsureReleaseEnvLeavesALocalBuildWithoutARelease(t *testing.T) {
	t.Serial()
	freshReleaseEnv(t)
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv(ReleaseEnv, "")
	require.NoError(t, ensureReleaseEnv())
	assert.Equal(t, "", os.Getenv(gosmoReleaseEnv))
}

func TestEveryAPEJobAsksForTheReleaseFirst(t *testing.T) {
	t.Serial()
	old := ensureReleaseEnvFunc
	ensureReleaseEnvFunc = func() error { return errors.New("buildhost said no") }
	defer func() { ensureReleaseEnvFunc = old }()
	_, err := forkBuildEnv{}.apeJob("example.com/m", "out")
	assert.ErrorContains(t, err, "buildhost said no")
}

func TestUsableReleaseRejectsWhatABuildSettingCannotCarry(t *testing.T) {
	t.Serial()
	v, err := usableRelease("17")
	require.NoError(t, err)
	assert.Equal(t, "17", v)
	_, err = usableRelease("1 7")
	assert.Error(t, err)
}

// fakeBuildhost serves the OIDC token, the run lock and the create-release route.
type fakeBuildhost struct {
	mu       sync.Mutex
	lock     string
	creates  int
	release  string
	project  string
	gotToken string
}

func (f *fakeBuildhost) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"value": "oidc-token"})
	})
	mux.HandleFunc("/api/v1/run-locks", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method == http.MethodGet {
			assert.Equal(t, releaseRunLock, r.URL.Query().Get("name"))
			json.NewEncoder(w).Encode(runLockBody{Found: f.lock != "", Value: f.lock})
			return
		}
		var body runLockBody
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		if f.lock == "" {
			f.lock = body.Value
		}
		json.NewEncoder(w).Encode(runLockBody{Found: true, Value: f.lock})
	})
	mux.HandleFunc("/api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.creates++
		f.gotToken = r.Header.Get("Authorization")
		f.project = strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/projects/"), "/releases")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"version": f.release})
	})
	return mux
}

func ciEnv(t *testing.T, server string) {
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv(runLockStoreEnv, server)
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", server+"/token")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_TOKEN", "request-token")
	t.Setenv("GITHUB_REPOSITORY", "wow-look-at-my/slopfix")
	t.Setenv("GITHUB_RUN_ID", "1")
	t.Setenv("GITHUB_RUN_ATTEMPT", "1")
	t.Setenv("GITHUB_SHA", "abc")
	t.Setenv("GITHUB_REF_NAME", "master")
	t.Setenv(ReleaseEnv, "")
}

func TestCreateRunReleaseCreatesTheReleaseAndLocksItForTheRun(t *testing.T) {
	t.Serial()
	fake := &fakeBuildhost{release: "231"}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	ciEnv(t, srv.URL)

	got, err := createRunRelease()
	require.NoError(t, err)
	assert.Equal(t, "231", got)
	assert.Equal(t, "slopfix", fake.project)
	assert.Equal(t, "Bearer oidc-token", fake.gotToken)
	assert.Equal(t, "231", fake.lock)

	// A second job of the same run reads the lock and creates nothing.
	got, err = createRunRelease()
	require.NoError(t, err)
	assert.Equal(t, "231", got)
	assert.Equal(t, 1, fake.creates)
}

func TestCreateRunReleaseTakesTheNumberAnotherJobLocked(t *testing.T) {
	t.Serial()
	fake := &fakeBuildhost{lock: "230", release: "999"}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	ciEnv(t, srv.URL)

	got, err := createRunRelease()
	require.NoError(t, err)
	assert.Equal(t, "230", got)
	assert.Equal(t, 0, fake.creates)
}

func TestCreateRunReleaseFailsLoudlyWithoutAnOIDCToken(t *testing.T) {
	t.Serial()
	ciEnv(t, "http://127.0.0.1:1")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	_, err := createRunRelease()
	assert.ErrorContains(t, err, "id-token: write")
}

func TestLookupReleaseOutsideCIHasNoRelease(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv(ReleaseEnv, "")
	got, err := lookupRelease()
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestLookupReleasePrefersTheExplicitNumber(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv(ReleaseEnv, " 55 ")
	got, err := lookupRelease()
	require.NoError(t, err)
	assert.Equal(t, "55", got)
}

func TestWriteReleaseOutputAppendsForThePublish(t *testing.T) {
	t.Serial()
	out := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.WriteFile(out, []byte("id=root\n"), 0o644))
	t.Setenv("GITHUB_OUTPUT", out)
	writeReleaseOutput("77")
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "id=root\nrelease_version=77\n", string(data))
}
