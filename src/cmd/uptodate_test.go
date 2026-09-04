package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// chdirTemp enters a fresh temp directory. An NT host will not delete a
// directory that is a process's cwd, so the restore has to happen.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

func TestComputeFingerprint(t *testing.T) {
	t.Serial()
	chdirTemp(t)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n\nfunc main() {}\n"), 0644)

	fp1, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.NotEmpty(t, fp1)

	// Same inputs -> same fingerprint
	fp2, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.Equal(t, fp1, fp2)

	// Change a file -> different fingerprint
	os.WriteFile("main.go", []byte("package main\n\nfunc main() { println(\"hi\") }\n"), 0644)
	fp3, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp3)
}

func TestComputeFingerprintIncludesGoSum(t *testing.T) {
	t.Serial()
	chdirTemp(t)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	fp1, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)

	// Adding go.sum changes fingerprint
	os.WriteFile("go.sum", []byte("example.com/dep v1.0.0 h1:abc=\n"), 0644)
	fp2, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp2)
}

// A source package named "build" is not the output directory. Skipping it by
// NAME hid every edit under this repo's own src/build, so the fast exit served
// a stale binary and called the run finished.
func TestComputeFingerprintCountsASourceDirNamedBuild(t *testing.T) {
	t.Serial()
	chdirTemp(t)
	old := outputDir
	outputDir = "build"
	defer func() { outputDir = old }()

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	require.NoError(t, os.MkdirAll("src/build", 0o755))
	os.WriteFile("src/build/target.go", []byte("package build\n"), 0644)

	fp1, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)

	os.WriteFile("src/build/target.go", []byte("package build\n\nconst X = 1\n"), 0644)
	fp2, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp2, "an edit under src/build must bust the fingerprint")

	// The output directory is the run's product, so it stays out.
	require.NoError(t, os.MkdirAll("build", 0o755))
	os.WriteFile("build/leftover.go", []byte("package main\n"), 0644)
	fp3, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.Equal(t, fp2, fp3, "the output directory is not an input")
}

// action.yml is test data (handoffname_test.go asserts its hand-off name
// templates) that no //go:embed can reach from src/cmd, so editing it has to
// bust the fingerprint -- otherwise the run fast-exits "Up to date" and those
// assertions never re-run locally.
func TestComputeFingerprintIncludesActionYML(t *testing.T) {
	t.Serial()
	chdirTemp(t)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)
	os.WriteFile("action.yml", []byte("name: x\nruns:\n  using: composite\n"), 0644)

	fp1, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)

	os.WriteFile("action.yml", []byte("name: y\nruns:\n  using: composite\n"), 0644)
	fp2, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp2, "an action.yml edit must bust the fingerprint")
}

// The environment decides what a run does — an env-gated test switched on is a
// pipeline the stored fingerprint never described — so the skip must not fire
// across a changed variable.
func TestComputeFingerprintIncludesTheEnvironment(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	runEnv = []string{"PATH=/usr/bin", "RUN_INTEGRATION_TESTS="}
	defer func() { runEnv = nil }()
	fp1, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)

	runEnv = []string{"PATH=/usr/bin", "RUN_INTEGRATION_TESTS=1"}
	fp2, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp2, "enabling an env-gated test must force a run")

	// Order is not a difference, nor is a variable the shell rewrites on every command line.
	runEnv = []string{"_=/usr/local/bin/go-toolchain", "RUN_INTEGRATION_TESTS=1", "SHLVL=3", "PATH=/usr/bin", "OLDPWD=/tmp"}
	fp3, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.Equal(t, fp2, fp3, "shell-churned variables must not bust the fingerprint")
}

// --generate runs go:generate directives; a stored fingerprint from a plain run
// does not describe that run, and skipping it reports success for generators
// that never executed.
func TestComputeFingerprintIncludesTheFlags(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	fp1, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)

	// PersistentFlags, not Flags: cobra folds a persistent flag into the local
	// set while it parses, so Flags() carries "generate" only once something
	// has run the root command -- which made this test depend on its neighbours.
	require.NoError(t, rootCmd.PersistentFlags().Set("generate", "deadbeef"))
	defer rootCmd.PersistentFlags().Set("generate", "")
	fp2, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp2, "a flag that changes what the run does must bust the fingerprint")
}

// A testdata fixture is read at run time by the test that would now fail, and
// no //go:embed covers it.
func TestComputeFingerprintIncludesTestdata(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	t.Chdir(dir)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)
	require.NoError(t, os.MkdirAll(filepath.Join("pkg", "testdata"), 0o755))
	fixture := filepath.Join("pkg", "testdata", "want.json")
	require.NoError(t, os.WriteFile(fixture, []byte(`{"n":1}`), 0o644))

	fp1, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(fixture, []byte(`{"n":2}`), 0o644))
	fp2, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.NotEqual(t, fp1, fp2, "a changed testdata fixture must force a run")
}

func TestComputeFingerprintSkipsBuildDir(t *testing.T) {
	t.Serial()
	chdirTemp(t)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	fp1, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)

	// Adding files in build/ should not change fingerprint
	os.MkdirAll("build", 0755)
	os.WriteFile("build/binary.go", []byte("package main\n"), 0644)
	fp2, err := computeFingerprint(runner.NewMock())
	require.NoError(t, err)
	assert.Equal(t, fp1, fp2)
}

func TestFingerprintFile(t *testing.T) {
	t.Serial()
	chdirTemp(t)

	fp := fingerprintFile()
	assert.Contains(t, fp, "go-toolchain-fingerprint")
	assert.True(t, filepath.IsAbs(fp))
}

func TestIsUpToDateNoFingerprint(t *testing.T) {
	t.Serial()
	chdirTemp(t)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	// No stored fingerprint -> not up to date
	assert.False(t, isUpToDate(runner.NewMock()))
}

func TestIsUpToDateWithMatchingFingerprint(t *testing.T) {
	t.Serial()
	chdirTemp(t)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.MkdirAll("src", 0755)
	os.WriteFile("src/main.go", []byte("package main\nfunc main() {}\n"), 0644)

	// Create build output
	os.MkdirAll("build", 0755)
	os.WriteFile("build/example.com", []byte("binary"), 0755)

	// Save fingerprint
	saveFingerprint(runner.NewMock())

	// Should be up to date now
	assert.True(t, isUpToDate(runner.NewMock()))
}

func TestIsUpToDateStaleAfterChange(t *testing.T) {
	t.Serial()
	chdirTemp(t)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.MkdirAll("src", 0755)
	os.WriteFile("src/main.go", []byte("package main\nfunc main() {}\n"), 0644)

	// Create build output
	os.MkdirAll("build", 0755)
	os.WriteFile("build/example.com", []byte("binary"), 0755)

	saveFingerprint(runner.NewMock())

	// Modify source -> stale
	os.WriteFile("src/main.go", []byte("package main\nfunc main() { println(\"changed\") }\n"), 0644)
	assert.False(t, isUpToDate(runner.NewMock()))
}

func TestSaveFingerprint(t *testing.T) {
	t.Serial()
	chdirTemp(t)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	saveFingerprint(runner.NewMock())

	fp := fingerprintFile()
	data, err := os.ReadFile(fp)
	require.NoError(t, err)
	assert.NotEmpty(t, data)
	assert.Len(t, string(data), 64) // the hex width of a sha256 digest
}

// listPkg mirrors the subset of `go list -json` output that embeddedFiles reads.
type listPkg struct {
	Dir             string   `json:"Dir"`
	EmbedFiles      []string `json:"EmbedFiles,omitempty"`
	TestEmbedFiles  []string `json:"TestEmbedFiles,omitempty"`
	XTestEmbedFiles []string `json:"XTestEmbedFiles,omitempty"`
}

// mockGoListRunner returns a runner that answers `go list -test -json ./...`
// with the given packages encoded as the stream of JSON objects go list emits.
func mockGoListRunner(pkgs ...listPkg) *runner.Mock {
	var out []byte
	for _, p := range pkgs {
		b, _ := json.Marshal(p)
		out = append(out, b...)
		out = append(out, '\n')
	}
	mock := runner.NewMock()
	mock.SetResponse("go", []string{"list", "-test", "-json", "./..."}, out, nil)
	return mock
}

func TestEmbeddedFilesParsesAllThreeFields(t *testing.T) {
	t.Serial()
	mock := mockGoListRunner(
		listPkg{Dir: "/m", EmbedFiles: []string{"a.txt", "static/app.js"}},
		listPkg{Dir: "/m/sub", TestEmbedFiles: []string{"t.txt"}, XTestEmbedFiles: []string{"x.txt"}},
		// Same file embedded by another package must collapse to a single entry.
		listPkg{Dir: "/m", EmbedFiles: []string{"a.txt"}},
		// A package with no embeds at all contributes nothing.
		listPkg{Dir: "/m/none"},
	)

	got, err := embeddedFiles(mock)
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.FromSlash("/m/a.txt"),
		filepath.FromSlash("/m/static/app.js"),
		filepath.FromSlash("/m/sub/t.txt"),
		filepath.FromSlash("/m/sub/x.txt"),
	}, got)
}

func TestEmbeddedFilesGoListError(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()
	mock.SetResponse("go", []string{"list", "-test", "-json", "./..."}, nil, fmt.Errorf("build broken"))

	_, err := embeddedFiles(mock)
	require.Error(t, err, "a go list failure must propagate so the caller declines to short-circuit")
}

func TestComputeFingerprintFoldsEmbeds(t *testing.T) {
	t.Serial()
	dir := chdirTemp(t)

	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.WriteFile("main.go", []byte("package main\n"), 0644)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "asset.txt"), []byte("v1"), 0644))

	// A runner reports asset.txt as an embedded file; the other reports none.
	withEmbed := mockGoListRunner(listPkg{Dir: dir, EmbedFiles: []string{"asset.txt"}})
	noEmbed := runner.NewMock()

	base, err := computeFingerprint(withEmbed)
	require.NoError(t, err)

	// When asset.txt is tracked as an embed, changing its content must change the fingerprint.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "asset.txt"), []byte("v2"), 0644))
	changed, err := computeFingerprint(withEmbed)
	require.NoError(t, err)
	assert.NotEqual(t, base, changed, "embed content change must bust the fingerprint")

	// When nothing embeds it, the same data file is invisible to the fingerprint.
	fpA, err := computeFingerprint(noEmbed)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "asset.txt"), []byte("v3"), 0644))
	fpB, err := computeFingerprint(noEmbed)
	require.NoError(t, err)
	assert.Equal(t, fpA, fpB, "a non-embedded data file must not affect the fingerprint")
}

// TestUpToDateTracksEmbeddedFiles is the end-to-end regression for the bug: it
// drives real `go list` resolution over a fixture module that embeds data files
// via every directive form (EmbedFiles, TestEmbedFiles, XTestEmbedFiles)
// and asserts that editing any embedded file busts the "up to date" skip,
// while an unchanged tree still reports up to date.
func TestUpToDateTracksEmbeddedFiles(t *testing.T) {
	t.Serial()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	// Keep go list hermetic: never download a toolchain, ignore any workspace.
	t.Setenv("GOTOOLCHAIN", "local")
	t.Setenv("GOWORK", "off")

	dir := t.TempDir()
	t.Chdir(dir)

	write := func(name, content string) {
		p := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	}

	write("go.mod", "module example.com/embedfix\n\ngo 1.21\n")
	// Non-test code embedding asset.txt -> EmbedFiles.
	write("main.go", "package main\n\nimport _ \"embed\"\n\n//go:embed asset.txt\nvar asset string\n\nfunc main() { _ = asset }\n")
	write("asset.txt", "asset-v1\n")
	// In-package test embedding intest.txt -> TestEmbedFiles.
	write("main_test.go", "package main\n\nimport _ \"embed\"\n\n//go:embed intest.txt\nvar inTest string\n\nvar _ = inTest\n")
	write("intest.txt", "intest-v1\n")
	// External (pkg_test) test embedding xtest.txt -> XTestEmbedFiles.
	write("xmain_test.go", "package main_test\n\nimport _ \"embed\"\n\n//go:embed xtest.txt\nvar xTest string\n\nvar _ = xTest\n")
	write("xtest.txt", "xtest-v1\n")
	// The build output must exist for isUpToDate's output-existence check.
	write(filepath.Join("build", "embedfix"), "binary")

	r := runner.New()

	// Baseline: a no-op run reports up to date.
	saveFingerprint(r)
	require.True(t, isUpToDate(r), "fresh baseline must report up to date")

	// Each embed form, changed in isolation, must force a full run.
	for _, embed := range []string{"asset.txt", "intest.txt", "xtest.txt"} {
		write(embed, "changed-"+embed+"\n")
		assert.Falsef(t, isUpToDate(r), "changing embedded %s must force a full run", embed)

		// Re-baseline with the new content; a subsequent no-op run is up to date.
		saveFingerprint(r)
		require.Truef(t, isUpToDate(r), "after re-baseline, a no-op run on %s must report up to date", embed)
	}
}
