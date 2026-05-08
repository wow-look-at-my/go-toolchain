package cmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	selfupdate "github.com/wow-look-at-my/go-selfupdate-mini"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// testPkgName is the npm package name for go-toolchain, used only in tests to
// construct mock server URL paths. Production code derives this automatically
// via selfupdate.NpmRepositoryFromBuildInfo().
const testPkgName = "go-toolchain"

// makeRelease builds a minimal selfupdate.Release for testing isNewer.
func makeRelease(version string) *selfupdate.Release {
	return &selfupdate.Release{
		Version: selfupdate.Version{
			Original: "v" + version,
			Version:  version,
		},
	}
}

type mockUpdater struct {
	version    string
	found      bool
	detectErr  error
	newer      bool
	applyErr   error
	applyCalls int
}

func (m *mockUpdater) detect(_ context.Context, slug string) (string, bool, error) {
	return m.version, m.found, m.detectErr
}

func (m *mockUpdater) isNewer(currentVersion string) bool {
	return m.newer
}

func (m *mockUpdater) applyUpdate(_ context.Context, exePath string) error {
	m.applyCalls++
	return m.applyErr
}

func TestDoUpdate_DetectError(t *testing.T) {
	m := &mockUpdater{detectErr: fmt.Errorf("network down")}
	err := doUpdate(context.Background(), m)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to detect latest release")
	assert.Contains(t, err.Error(), "network down")
}

func TestDoUpdate_NotFound(t *testing.T) {
	m := &mockUpdater{found: false}
	err := doUpdate(context.Background(), m)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "no release found")
}

func TestDoUpdate_AlreadyUpToDate(t *testing.T) {
	old := buildVersion
	buildVersion = "v0.0.100"
	defer func() { buildVersion = old }()

	m := &mockUpdater{version: "v0.0.100", found: true, newer: false}
	err := doUpdate(context.Background(), m)
	require.Nil(t, err)
	assert.Equal(t, 0, m.applyCalls)
}

func TestDoUpdate_DevBuildWarning(t *testing.T) {
	old := buildVersion
	buildVersion = "dev"
	defer func() { buildVersion = old }()

	// Dev builds proceed to update (applyUpdate will be called)
	m := &mockUpdater{version: "v0.0.200", found: true, newer: false}
	// applyUpdate will succeed but exePath resolution uses the test binary
	err := doUpdate(context.Background(), m)
	// It may error on the actual binary update, but the dev build path was taken
	if err == nil {
		assert.Equal(t, 1, m.applyCalls)
	}
}

func TestDoUpdate_NonSemverVersion(t *testing.T) {
	old := buildVersion
	buildVersion = "latest-1-g649dd4a"
	defer func() { buildVersion = old }()

	// Non-semver versions should not panic and should proceed with update
	m := &mockUpdater{version: "v0.0.200", found: true, newer: true}
	err := doUpdate(context.Background(), m)
	// Should not panic; update proceeds
	if err == nil {
		assert.Equal(t, 1, m.applyCalls)
	}
}

// TestDoUpdate_BareShortSHA guards against regressing on a Masterminds/semver
// NewVersion() quirk: it leniently accepts a bare number like "0648669" (a
// git short SHA of all digits) as major=648669, which incorrectly compares
// as newer than any real "v0.0.N" release. The fix is to parse strictly so
// bare SHAs are treated as non-semver and the update proceeds.
func TestDoUpdate_BareShortSHA(t *testing.T) {
	old := buildVersion
	buildVersion = "0648669"
	defer func() { buildVersion = old }()

	m := &mockUpdater{version: "v0.0.1776682440", found: true, newer: false}
	err := doUpdate(context.Background(), m)
	if err == nil {
		assert.Equal(t, 1, m.applyCalls, "update should proceed for bare short SHA")
	}
}

func TestIsNewer_BareShortSHA(t *testing.T) {
	u := &npmUpdater{}
	u.latest = makeRelease("0.0.1776682440")
	assert.True(t, u.isNewer("0648669"),
		"bare short SHA must be treated as older than any real release")
}

func TestIsNewer_Semver(t *testing.T) {
	u := &npmUpdater{}
	u.latest = makeRelease("0.0.200")
	assert.True(t, u.isNewer("v0.0.100"), "v0.0.100 < v0.0.200")
	assert.False(t, u.isNewer("v0.0.200"), "v0.0.200 == v0.0.200")
	assert.False(t, u.isNewer("v0.0.300"), "v0.0.300 > v0.0.200")
}

func TestDoUpdate_ApplyError(t *testing.T) {
	old := buildVersion
	buildVersion = "v0.0.1"
	defer func() { buildVersion = old }()

	m := &mockUpdater{
		version:  "v0.0.200",
		found:    true,
		newer:    true,
		applyErr: fmt.Errorf("permission denied"),
	}
	err := doUpdate(context.Background(), m)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "update failed")
	assert.Contains(t, err.Error(), "permission denied")
}

func TestDoUpdate_Success(t *testing.T) {
	old := buildVersion
	buildVersion = "v0.0.1"
	defer func() { buildVersion = old }()

	m := &mockUpdater{version: "v0.0.200", found: true, newer: true}
	err := doUpdate(context.Background(), m)
	require.Nil(t, err)
	assert.Equal(t, 1, m.applyCalls)
}

// npmOS and npmArch map Go platform names to npm package-name components.
func testNpmOS() string {
	if runtime.GOOS == "windows" {
		return "win32"
	}
	return runtime.GOOS
}

func testNpmArch() string {
	if runtime.GOARCH == "amd64" {
		return "x64"
	}
	return runtime.GOARCH
}

// makeFakeNpmServer returns a test server that serves npm-registry-style JSON
// for the platform-specific package, plus a tarball containing a fake binary.
// version must be a bare semver (no "v" prefix).
func makeFakeNpmServer(t *testing.T, version string) *httptest.Server {
	t.Helper()
	platPkg := testPkgName + "-" + testNpmOS() + "-" + testNpmArch()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/@wow-look-at-my%2F"+platPkg ||
			r.URL.Path == "/@wow-look-at-my/"+platPkg:
			tarballURL := srv.URL + "/tarball/" + version + ".tgz"
			fmt.Fprintf(w, `{
				"dist-tags": {"latest": %q},
				"versions": {%q: {"dist": {"tarball": %q}}}
			}`, version, version, tarballURL)

		case r.URL.Path == "/tarball/"+version+".tgz":
			w.Header().Set("Content-Type", "application/octet-stream")
			writeFakeTarball(t, w)

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// writeFakeTarball writes a .tgz containing a tiny fake binary at the
// expected path (package/bin/go-toolchain or .exe on Windows).
func writeFakeTarball(t *testing.T, w http.ResponseWriter) {
	t.Helper()
	binName := testPkgName
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	content := []byte("#!/bin/sh\necho fake\n")

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name: "package/bin/" + binName,
		Mode: 0755,
		Size: int64(len(content)),
	}
	require.Nil(t, tw.WriteHeader(hdr))
	_, err := tw.Write(content)
	require.Nil(t, err)
	require.Nil(t, tw.Close())
	require.Nil(t, gz.Close())
	w.Write(buf.Bytes()) //nolint:errcheck
}

func TestNpmUpdaterDetect(t *testing.T) {
	srv := makeFakeNpmServer(t, "0.0.100")
	u := &npmUpdater{registryBase: srv.URL + "/"}

	version, found, err := u.detect(context.Background(), "")
	require.Nil(t, err)
	assert.True(t, found)
	assert.Equal(t, "v0.0.100", version)
	assert.NotNil(t, u.latest)
}

func TestNpmUpdaterDetect_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	u := &npmUpdater{registryBase: srv.URL + "/"}
	_, found, err := u.detect(context.Background(), "")
	require.Nil(t, err)
	assert.False(t, found)
}

func TestNpmUpdaterApplyUpdate(t *testing.T) {
	srv := makeFakeNpmServer(t, "0.0.200")
	u := &npmUpdater{registryBase: srv.URL + "/"}
	_, found, err := u.detect(context.Background(), "")
	require.Nil(t, err)
	require.True(t, found)

	// Write a placeholder "binary" to a temp dir and apply the update.
	dir := t.TempDir()
	target := filepath.Join(dir, testPkgName)
	require.Nil(t, os.WriteFile(target, []byte("old"), 0755))

	err = u.applyUpdate(context.Background(), target)
	require.Nil(t, err)

	got, err := os.ReadFile(target)
	require.Nil(t, err)
	assert.Equal(t, "#!/bin/sh\necho fake\n", string(got))
}

func TestNpmUpdaterApplyUpdate_BadTarball(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not a tarball")) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)

	// Build a minimal npmUpdater with a detect result pointing at our bad server.
	badSrv := makeBadTarballServer(t, srv.URL)
	u := &npmUpdater{registryBase: badSrv.URL + "/"}
	_, found, err := u.detect(context.Background(), "")
	require.Nil(t, err)
	require.True(t, found)

	err = u.applyUpdate(context.Background(), "/tmp/nowhere")
	require.NotNil(t, err)
}

func makeBadTarballServer(t *testing.T, badTarballURL string) *httptest.Server {
	t.Helper()
	platPkg := testPkgName + "-" + testNpmOS() + "-" + testNpmArch()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/@wow-look-at-my%2F"+platPkg ||
			r.URL.Path == "/@wow-look-at-my/"+platPkg:
			fmt.Fprintf(w, `{
				"dist-tags": {"latest": "0.0.1"},
				"versions": {"0.0.1": {"dist": {"tarball": %q}}}
			}`, badTarballURL+"/bad.tgz")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
