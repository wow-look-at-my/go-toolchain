package cmd

import (
	"encoding/binary"
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
	"github.com/wow-look-at-my/go-toolchain/src/runner"
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
	require.NoError(t, ensureReleaseEnv("slopfix"))
	assert.Equal(t, "412", os.Getenv(gosmoReleaseEnv))
	assert.Equal(t, "660ecca3f00d8e1b2c3d4e5f60718293a4b5c6d7", os.Getenv(gosmoReleaseRevisionEnv))
}

func TestEnsureReleaseEnvFailsTheBuildWhenCICannotGetANumber(t *testing.T) {
	t.Serial()
	freshReleaseEnv(t)
	ciEnv(t, "http://127.0.0.1:1")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	require.ErrorContains(t, ensureReleaseEnv("slopfix"), "id-token: write")
	assert.Equal(t, "", os.Getenv(gosmoReleaseEnv))
}

func TestEnsureReleaseEnvLeavesALocalBuildWithoutARelease(t *testing.T) {
	t.Serial()
	freshReleaseEnv(t)
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv(ReleaseEnv, "")
	require.NoError(t, ensureReleaseEnv("slopfix"))
	assert.Equal(t, "", os.Getenv(gosmoReleaseEnv))
}

func TestEveryAPEJobAsksForTheReleaseFirst(t *testing.T) {
	t.Serial()
	old := ensureReleaseEnvFunc
	ensureReleaseEnvFunc = func(string) error { return errors.New("buildhost said no") }
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
	locks    map[string]string
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
		if f.locks == nil {
			f.locks = map[string]string{}
		}
		if r.Method == http.MethodGet {
			v := f.locks[r.URL.Query().Get("name")]
			json.NewEncoder(w).Encode(runLockBody{Found: v != "", Value: v})
			return
		}
		var body runLockBody
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		if f.locks[body.Name] == "" {
			f.locks[body.Name] = body.Value
		}
		json.NewEncoder(w).Encode(runLockBody{Found: true, Value: f.locks[body.Name]})
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

	got, err := createRunRelease("slopfix")
	require.NoError(t, err)
	assert.Equal(t, "231", got)
	assert.Equal(t, "slopfix", fake.project)
	assert.Equal(t, "Bearer oidc-token", fake.gotToken)
	assert.Equal(t, "231", fake.locks["buildhost-release:slopfix"])

	// A second job of the same run reads the lock and creates nothing.
	got, err = createRunRelease("slopfix")
	require.NoError(t, err)
	assert.Equal(t, "231", got)
	assert.Equal(t, 1, fake.creates)
}

// The release lives where the publish uploads. A binary named apart from its
// repo publishes to a child project, which numbers its releases on its own.
func TestCreateRunReleaseCreatesTheReleaseInTheBinarysPublishProject(t *testing.T) {
	t.Serial()
	fake := &fakeBuildhost{release: "7"}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	ciEnv(t, srv.URL)
	t.Setenv("GITHUB_REPOSITORY", "wow-look-at-my/webhooks")

	got, err := createRunRelease("webhook-runner")
	require.NoError(t, err)
	assert.Equal(t, "7", got)
	assert.Equal(t, "webhooks/webhook-runner", fake.project)
}

// Jobs of one run that publish to different projects each get that
// project's own number. A shared number collides with a release the other
// project already published.
func TestCreateRunReleaseLocksEachProjectApart(t *testing.T) {
	t.Serial()
	fake := &fakeBuildhost{release: "350"}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	ciEnv(t, srv.URL)
	t.Setenv("GITHUB_REPOSITORY", "wow-look-at-my/webhooks")

	got, err := createRunRelease("webhook-runner")
	require.NoError(t, err)
	assert.Equal(t, "350", got)

	fake.release = "662"
	got, err = createRunRelease("comment-guard")
	require.NoError(t, err)
	assert.Equal(t, "662", got)
	assert.Equal(t, "webhooks/comment-guard", fake.project)
	assert.Equal(t, 2, fake.creates)
	assert.Equal(t, map[string]string{
		"buildhost-release:webhooks/webhook-runner": "350",
		"buildhost-release:webhooks/comment-guard":  "662",
	}, fake.locks)
}

func TestPublishProjectMatchesTheBuildhostPublishRule(t *testing.T) {
	t.Serial()
	for _, c := range []struct{ repo, binary, want string }{
		{"slopfix", "slopfix", "slopfix"},
		{"slopfix", "", "slopfix"},
		{"buildhost", "buildhost-agent", "buildhost/agent"},
		{"webhooks", "webhook-runner", "webhooks/webhook-runner"},
		// A repository whose name carries capitals publishes under its lowercase project, and its binary's name is lowercase too.
		{"VictoriaLogs-compat-layer", "VictoriaLogs-compat-layer", "victorialogs-compat-layer"},
		{"VictoriaLogs-compat-layer", "victorialogs-compat-layer-cli", "victorialogs-compat-layer/cli"},
	} {
		assert.Equal(t, c.want, publishProject(c.repo, c.binary), "%s + %s", c.repo, c.binary)
	}
}

func TestEveryAPEJobNamesItsBinaryForTheRelease(t *testing.T) {
	t.Serial()
	old := ensureReleaseEnvFunc
	var got string
	ensureReleaseEnvFunc = func(binary string) error { got = binary; return nil }
	defer func() { ensureReleaseEnvFunc = old }()
	_, err := forkBuildEnv{}.apeJob("example.com/m/cmd/webhook-runner", filepath.Join("build", "webhook-runner"))
	require.NoError(t, err)
	assert.Equal(t, "webhook-runner", got)
}

func TestCreateRunReleaseTakesTheNumberAnotherJobLocked(t *testing.T) {
	t.Serial()
	fake := &fakeBuildhost{locks: map[string]string{"buildhost-release:slopfix": "230"}, release: "999"}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	ciEnv(t, srv.URL)

	got, err := createRunRelease("slopfix")
	require.NoError(t, err)
	assert.Equal(t, "230", got)
	assert.Equal(t, 0, fake.creates)
}

func TestCreateRunReleaseFailsLoudlyWithoutAnOIDCToken(t *testing.T) {
	t.Serial()
	ciEnv(t, "http://127.0.0.1:1")
	t.Setenv("ACTIONS_ID_TOKEN_REQUEST_URL", "")
	_, err := createRunRelease("slopfix")
	assert.ErrorContains(t, err, "id-token: write")
}

func TestLookupReleaseOutsideCIHasNoRelease(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_ACTIONS", "")
	t.Setenv(ReleaseEnv, "")
	got, err := lookupRelease("slopfix")
	require.NoError(t, err)
	assert.Equal(t, "", got)
}

func TestLookupReleasePrefersTheExplicitNumber(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv(ReleaseEnv, " 55 ")
	got, err := lookupRelease("slopfix")
	require.NoError(t, err)
	assert.Equal(t, "55", got)
}

func TestWriteReleaseOutputAppendsForThePublish(t *testing.T) {
	t.Serial()
	out := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.WriteFile(out, []byte("id=root\n"), 0o644))
	t.Setenv("GITHUB_OUTPUT", out)
	require.NoError(t, writeReleaseOutput("77"))
	data, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, "id=root\nrelease_version=77\n", string(data))
}

func TestWriteReleaseOutputFailsWhenThePublishCannotReadIt(t *testing.T) {
	t.Serial()
	t.Setenv("GITHUB_OUTPUT", filepath.Join(t.TempDir(), "missing-dir", "output"))
	assert.ErrorContains(t, writeReleaseOutput("77"), "GITHUB_OUTPUT")
}

func TestBuildInfoSettingsReadsTheBuildLines(t *testing.T) {
	t.Serial()
	out := "build/slopfix: go1.26\n\tpath\tgithub.com/x/slopfix\n\tbuild\t-trimpath=true\n\tbuild\trelease=412\n\tbuild\trelease.revision=abc\n"
	got := buildInfoSettings(out)
	assert.Equal(t, "412", got["release"])
	assert.Equal(t, "abc", got["release.revision"])
	assert.Equal(t, "true", got["-trimpath"])
	assert.NotContains(t, got, "path")
}

func stampJob() buildJob {
	return buildJob{goCmd: []string{"/fork/bin/go"}, goroot: "/fork"}
}

func TestVerifyReleaseStampPassesAStampedBinary(t *testing.T) {
	t.Serial()
	t.Setenv(gosmoReleaseEnv, "412")
	t.Setenv(gosmoReleaseRevisionEnv, "abc")
	m := runner.NewMock()
	m.SetResponse("/fork/bin/go", []string{"version", "-m", "out"}, []byte("out: go1.26\n\tbuild\trelease=412\n\tbuild\trelease.revision=abc\n"), nil)
	require.NoError(t, verifyReleaseStamp(m, stampJob(), "out"))
}

func TestVerifyReleaseStampFailsABinaryTheGoCommandDidNotStamp(t *testing.T) {
	t.Serial()
	t.Setenv(gosmoReleaseEnv, "412")
	t.Setenv(gosmoReleaseRevisionEnv, "abc")
	m := runner.NewMock()
	m.SetResponse("/fork/bin/go", []string{"version", "-m", "out"}, []byte("out: go1.26\n\tbuild\t-trimpath=true\n"), nil)
	assert.ErrorContains(t, verifyReleaseStamp(m, stampJob(), "out"), "did not stamp the release")
}

func TestVerifyReleaseStampFailsAnotherNumber(t *testing.T) {
	t.Serial()
	t.Setenv(gosmoReleaseEnv, "412")
	t.Setenv(gosmoReleaseRevisionEnv, "abc")
	m := runner.NewMock()
	m.SetResponse("/fork/bin/go", []string{"version", "-m", "out"}, []byte("\tbuild\trelease=411\n\tbuild\trelease.revision=abc\n"), nil)
	assert.Error(t, verifyReleaseStamp(m, stampJob(), "out"))
}

// A library-only module builds each package to an archive, which carries no build info.
func TestVerifyReleaseStampSkipsAPackageArchive(t *testing.T) {
	t.Serial()
	t.Setenv(gosmoReleaseEnv, "412")
	t.Setenv(gosmoReleaseRevisionEnv, "abc")
	path := filepath.Join(t.TempDir(), "cachedisk")
	require.NoError(t, os.WriteFile(path, []byte(packageArchiveMagic+"__.PKGDEF"), 0o644))
	require.NoError(t, verifyReleaseStamp(runner.NewMock(), stampJob(), path))
}

func TestVerifyReleaseStampReadsNothingWithoutARelease(t *testing.T) {
	t.Serial()
	t.Setenv(gosmoReleaseEnv, "")
	require.NoError(t, verifyReleaseStamp(runner.NewMock(), stampJob(), "out"))
}

// wasmWithBuildInfo writes a WebAssembly module whose bytes carry an inline build-info blob with the given modinfo lines.
func wasmWithBuildInfo(t *testing.T, modinfo string) string {
	t.Helper()
	frame := strings.Repeat("\xff", buildInfoFrame)
	mod := frame + modinfo + frame
	header := make([]byte, buildInfoHeaderSize)
	copy(header, buildInfoMagic)
	header[14] = 8
	header[buildInfoFlagsOffset] = buildInfoFlagInline
	var blob []byte
	blob = append(blob, header...)
	blob = binary.AppendUvarint(blob, uint64(len("go1.26")))
	blob = append(blob, "go1.26"...)
	blob = binary.AppendUvarint(blob, uint64(len(mod)))
	blob = append(blob, mod...)
	path := filepath.Join(t.TempDir(), "app_js_wasm")
	body := append([]byte(wasmMagic+"\x01\x00\x00\x00some section bytes"), blob...)
	require.NoError(t, os.WriteFile(path, append(body, "trailing bytes"...), 0o755))
	return path
}

// A wasm module is read off its bytes: the mock go command answers nothing, so a `go version -m` here would fail.
func TestVerifyReleaseStampReadsAWasmModuleOffItsBytes(t *testing.T) {
	t.Serial()
	t.Setenv(gosmoReleaseEnv, "412")
	t.Setenv(gosmoReleaseRevisionEnv, "abc")
	stamped := wasmWithBuildInfo(t, "path\tgithub.com/x/app\nbuild\t-trimpath=true\nbuild\trelease=412\nbuild\trelease.revision=abc\n")
	require.NoError(t, verifyReleaseStamp(runner.NewMock(), stampJob(), stamped))

	other := wasmWithBuildInfo(t, "path\tgithub.com/x/app\nbuild\trelease=411\nbuild\trelease.revision=abc\n")
	assert.ErrorContains(t, verifyReleaseStamp(runner.NewMock(), stampJob(), other), "did not stamp the release")

	// What the js/wasm linker writes: no build-info header, only the modinfo between its sentinels.
	framed := func(modinfo string) string {
		path := filepath.Join(t.TempDir(), "framed_js_wasm")
		body := wasmMagic + "\x01\x00\x00\x00data" + modinfoStart + modinfo + modinfoEnd + "more data"
		require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
		return path
	}
	require.NoError(t, verifyReleaseStamp(runner.NewMock(), stampJob(), framed("path\tgithub.com/x/app\nbuild\trelease=412\nbuild\trelease.revision=abc\n")))
	assert.ErrorContains(t, verifyReleaseStamp(runner.NewMock(), stampJob(), framed("path\tgithub.com/x/app\nbuild\trelease=411\nbuild\trelease.revision=abc\n")), "did not stamp the release")

	bare := filepath.Join(t.TempDir(), "bare_js_wasm")
	require.NoError(t, os.WriteFile(bare, []byte(wasmMagic+"\x01\x00\x00\x00no build info here"), 0o755))
	assert.ErrorContains(t, verifyReleaseStamp(runner.NewMock(), stampJob(), bare), "no Go build info")
}
