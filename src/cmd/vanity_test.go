package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/mod/modfile"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectVanityReplacesNoGoSum(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	state, err := injectVanityReplaces()
	assert.Nil(t, err)
	assert.Nil(t, state)
}

func TestInjectVanityReplacesAllReachable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	gosum := `gotest.tools/gotestsum v1.13.0 h1:aaa=
gotest.tools/gotestsum v1.13.0/go.mod h1:bbb=
`
	os.WriteFile("go.sum", []byte(gosum), 0644)
	os.WriteFile("go.mod", []byte("module test\ngo 1.21\n"), 0644)

	// All hosts reachable → no replaces needed
	old := vanityHostChecker
	vanityHostChecker = func(host string) bool { return true }
	defer func() { vanityHostChecker = old }()

	state, err := injectVanityReplaces()
	assert.Nil(t, err)
	assert.Nil(t, state)
}

func TestInjectAndRemoveVanityReplaces(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	gomod := "module test\n\ngo 1.21\n\nrequire gotest.tools/gotestsum v1.13.0\n"
	os.WriteFile("go.mod", []byte(gomod), 0644)

	gosum := `gotest.tools/gotestsum v1.13.0 h1:aaa=
gotest.tools/gotestsum v1.13.0/go.mod h1:bbb=
`
	os.WriteFile("go.sum", []byte(gosum), 0644)

	// Host unreachable, VCS resolver returns GitHub URL
	oldChecker := vanityHostChecker
	vanityHostChecker = func(host string) bool { return false }
	defer func() { vanityHostChecker = oldChecker }()

	oldResolver := vanityVCSResolver
	vanityVCSResolver = func(modulePath, version string) (string, string, error) {
		if modulePath == "gotest.tools/gotestsum" {
			return "https://github.com/gotestyourself/gotestsum", modulePath, nil
		}
		return "", "", os.ErrNotExist
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	// Inject
	state, err := injectVanityReplaces()
	require.Nil(t, err)
	require.NotNil(t, state)
	require.Equal(t, 1, len(state.Replaces))
	assert.Equal(t, "gotest.tools/gotestsum", state.Replaces[0].OldPath)
	assert.Equal(t, "github.com/gotestyourself/gotestsum", state.Replaces[0].NewPath)
	assert.Equal(t, gosum, string(state.OrigGoSum))

	// Verify go.mod has the replace directive
	data, _ := os.ReadFile("go.mod")
	content := string(data)
	assert.Contains(t, content, "replace gotest.tools/gotestsum")
	assert.Contains(t, content, "github.com/gotestyourself/gotestsum")

	// Simulate go mod tidy swapping vanity entries for replacement entries while the replace is active.
	os.WriteFile("go.sum", []byte("github.com/gotestyourself/gotestsum v1.13.0 h1:xxx=\n"), 0644)

	// Remove
	err = removeVanityReplaces(state)
	require.Nil(t, err)

	// Verify replace is gone
	data, _ = os.ReadFile("go.mod")
	content = string(data)
	assert.NotContains(t, content, "replace")
	// Original require should still be there
	assert.Contains(t, content, "gotest.tools/gotestsum")

	// go.sum should be restored to its pre-injection state
	restored, _ := os.ReadFile("go.sum")
	assert.Equal(t, gosum, string(restored))
}

func TestInjectVanityReplacesMultipleModulesSameHost(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	gomod := "module test\n\ngo 1.21\n\nrequire (\n\tmodernc.org/sqlite v1.45.0\n\tmodernc.org/libc v1.67.6\n)\n"
	os.WriteFile("go.mod", []byte(gomod), 0644)

	gosum := `modernc.org/sqlite v1.45.0 h1:aaa=
modernc.org/sqlite v1.45.0/go.mod h1:bbb=
modernc.org/libc v1.67.6 h1:ccc=
modernc.org/libc v1.67.6/go.mod h1:ddd=
`
	os.WriteFile("go.sum", []byte(gosum), 0644)

	oldChecker := vanityHostChecker
	vanityHostChecker = func(host string) bool { return false }
	defer func() { vanityHostChecker = oldChecker }()

	oldResolver := vanityVCSResolver
	vanityVCSResolver = func(modulePath, version string) (string, string, error) {
		mapping := map[string]string{
			"modernc.org/sqlite": "https://gitlab.com/cznic/sqlite",
			"modernc.org/libc":   "https://gitlab.com/cznic/libc",
		}
		if url, ok := mapping[modulePath]; ok {
			return url, modulePath, nil
		}
		return "", "", os.ErrNotExist
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	state, err := injectVanityReplaces()
	require.Nil(t, err)
	require.NotNil(t, state)
	assert.Equal(t, 2, len(state.Replaces))

	// Verify both replaces are in go.mod
	data, _ := os.ReadFile("go.mod")
	content := string(data)
	assert.Contains(t, content, "gitlab.com/cznic/sqlite")
	assert.Contains(t, content, "gitlab.com/cznic/libc")

	// Clean up
	err = removeVanityReplaces(state)
	require.Nil(t, err)

	data, _ = os.ReadFile("go.mod")
	assert.NotContains(t, string(data), "replace")
}

func TestInjectVanityReplacesSkipsUnresolvable(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	gomod := "module test\n\ngo 1.21\n\nrequire gotest.tools/gotestsum v1.13.0\n"
	os.WriteFile("go.mod", []byte(gomod), 0644)
	gosum := `gotest.tools/gotestsum v1.13.0 h1:aaa=
`
	os.WriteFile("go.sum", []byte(gosum), 0644)

	oldChecker := vanityHostChecker
	vanityHostChecker = func(host string) bool { return false }
	defer func() { vanityHostChecker = oldChecker }()

	// Resolver fails for everything
	oldResolver := vanityVCSResolver
	vanityVCSResolver = func(modulePath, version string) (string, string, error) {
		return "", "", os.ErrNotExist
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	state, err := injectVanityReplaces()
	assert.Nil(t, err)
	assert.Nil(t, state)

	// go.mod should be unchanged
	data, _ := os.ReadFile("go.mod")
	assert.NotContains(t, string(data), "replace")
}

func TestInjectVanityReplacesAppendsVersionSuffix(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	gomod := "module test\n\ngo 1.21\n\nrequire go.yaml.in/yaml/v3 v3.0.4\n"
	os.WriteFile("go.mod", []byte(gomod), 0644)
	gosum := "go.yaml.in/yaml/v3 v3.0.4 h1:aaa=\n"
	os.WriteFile("go.sum", []byte(gosum), 0644)

	oldChecker := vanityHostChecker
	vanityHostChecker = func(host string) bool { return false }
	defer func() { vanityHostChecker = oldChecker }()

	oldResolver := vanityVCSResolver
	vanityVCSResolver = func(modulePath, version string) (string, string, error) {
		return "https://github.com/yaml/go-yaml", "go.yaml.in/yaml", nil
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	state, err := injectVanityReplaces()
	require.Nil(t, err)
	require.NotNil(t, state)
	require.Equal(t, 1, len(state.Replaces))

	// The replacement path must include /v3 suffix for the version to be valid
	assert.Equal(t, "github.com/yaml/go-yaml/v3", state.Replaces[0].NewPath)
	assert.Equal(t, "v3.0.4", state.Replaces[0].NewVersion)

	data, _ := os.ReadFile("go.mod")
	assert.Contains(t, string(data), "github.com/yaml/go-yaml/v3 v3.0.4")

	err = removeVanityReplaces(state)
	require.Nil(t, err)
}

func TestInjectVanityReplacesSubModule(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	gomod := "module test\n\ngo 1.21\n\nrequire (\n\tgo.opentelemetry.io/otel v1.35.0\n\tgo.opentelemetry.io/otel/trace v1.35.0\n\tgo.opentelemetry.io/otel/sdk v1.35.0\n)\n"
	os.WriteFile("go.mod", []byte(gomod), 0644)

	gosum := `go.opentelemetry.io/otel v1.35.0 h1:aaa=
go.opentelemetry.io/otel/trace v1.35.0 h1:bbb=
go.opentelemetry.io/otel/sdk v1.35.0 h1:ccc=
`
	os.WriteFile("go.sum", []byte(gosum), 0644)

	oldChecker := vanityHostChecker
	vanityHostChecker = func(host string) bool { return false }
	defer func() { vanityHostChecker = oldChecker }()

	oldResolver := vanityVCSResolver
	vanityVCSResolver = func(modulePath, version string) (string, string, error) {
		return "https://github.com/open-telemetry/opentelemetry-go", "go.opentelemetry.io/otel", nil
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	state, err := injectVanityReplaces()
	require.Nil(t, err)
	require.NotNil(t, state)
	require.Equal(t, 3, len(state.Replaces))

	replacePaths := map[string]string{}
	for _, r := range state.Replaces {
		replacePaths[r.OldPath] = r.NewPath
	}

	assert.Equal(t, "github.com/open-telemetry/opentelemetry-go", replacePaths["go.opentelemetry.io/otel"])
	assert.Equal(t, "github.com/open-telemetry/opentelemetry-go/trace", replacePaths["go.opentelemetry.io/otel/trace"])
	assert.Equal(t, "github.com/open-telemetry/opentelemetry-go/sdk", replacePaths["go.opentelemetry.io/otel/sdk"])

	data, _ := os.ReadFile("go.mod")
	content := string(data)
	assert.Contains(t, content, "github.com/open-telemetry/opentelemetry-go/trace")
	assert.Contains(t, content, "github.com/open-telemetry/opentelemetry-go/sdk")

	err = removeVanityReplaces(state)
	require.Nil(t, err)

	data, _ = os.ReadFile("go.mod")
	assert.NotContains(t, string(data), "replace")
}

func TestInjectVanityReplacesSkipsNonDirectHost(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	gomod := "module test\n\ngo 1.21\n\nrequire vanity.test/widget v1.2.3\n"
	os.WriteFile("go.mod", []byte(gomod), 0644)
	gosum := "vanity.test/widget v1.2.3 h1:aaa=\n"
	os.WriteFile("go.sum", []byte(gosum), 0644)

	oldChecker := vanityHostChecker
	vanityHostChecker = func(host string) bool { return false }
	defer func() { vanityHostChecker = oldChecker }()

	// go.googlesource.com is not a direct code host, so the replace must be skipped, not swapped.
	oldResolver := vanityVCSResolver
	vanityVCSResolver = func(modulePath, version string) (string, string, error) {
		return "https://go.googlesource.com/widget", "vanity.test/widget", nil
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	state, err := injectVanityReplaces()
	assert.Nil(t, err)
	assert.Nil(t, state)

	// go.mod must be untouched — no replace onto a non-direct host.
	data, _ := os.ReadFile("go.mod")
	assert.NotContains(t, string(data), "replace")
	assert.NotContains(t, string(data), "go.googlesource.com")
}
func TestRemoveVanityReplacesEmpty(t *testing.T) {
	err := removeVanityReplaces(nil)
	assert.Nil(t, err)
}

func TestInjectVanityReplacesPreservesExistingGoMod(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	// go.mod with existing replace directive
	gomod := `module test

go 1.21

require gotest.tools/gotestsum v1.13.0

replace example.com/existing => example.com/replacement v1.0.0
`
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644)
	gosum := `gotest.tools/gotestsum v1.13.0 h1:aaa=
`
	os.WriteFile(filepath.Join(dir, "go.sum"), []byte(gosum), 0644)

	oldChecker := vanityHostChecker
	vanityHostChecker = func(host string) bool { return false }
	defer func() { vanityHostChecker = oldChecker }()

	oldResolver := vanityVCSResolver
	vanityVCSResolver = func(modulePath, version string) (string, string, error) {
		return "https://github.com/gotestyourself/gotestsum", modulePath, nil
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	state, err := injectVanityReplaces()
	require.Nil(t, err)
	require.NotNil(t, state)
	require.Equal(t, 1, len(state.Replaces))

	// Existing replace should still be there
	data, _ := os.ReadFile("go.mod")
	content := string(data)
	assert.Contains(t, content, "example.com/existing")

	// Remove only injected replaces
	err = removeVanityReplaces(state)
	require.Nil(t, err)

	data, _ = os.ReadFile("go.mod")
	content = string(data)
	// Existing replace preserved, vanity replace removed
	assert.Contains(t, content, "example.com/existing")
	assert.NotContains(t, content, "gotestyourself")
}

// runVanityTestGit runs a git command in the current directory, failing the
// test with the command's combined output on error.
func runVanityTestGit(t *testing.T, args ...string) {
	t.Helper()
	out, err := exec.Command("git", args...).CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
}

// TestCheckDirtyInCIWithVanityRestored pins the invariant that broke
// a github-state-mirror CI run: while vanity replaces are active (a
// vanity host was unreachable, so go.mod carries injected replace directives
// and go mod tidy rewrote go.sum onto the mirror paths), the post-vet CI
// dirty check must pass on a canonically tidy tree — the mutation is the
// toolchain's own and is removed before the run ends — while real
// uncommitted changes still fail, the active mirror state survives the check
// for the phases behind it, and the final cleanup leaves the committed tree
// byte-identical.
func TestCheckDirtyInCIWithVanityRestored(t *testing.T) {
	// Hermetic git: host/user config must not leak into the test repo.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir := t.TempDir()
	// Best-effort: a prior test can leave the process cwd deleted, making Getwd fail.
	t.Chdir(dir)

	// modfile-canonical form so removeVanityReplaces's parse-drop-format round trip restores the bytes exactly.
	parsed, err := modfile.Parse("go.mod", []byte("module test\ngo 1.21\nrequire gotest.tools/gotestsum v1.13.0\n"), nil)
	require.NoError(t, err)
	gomod, err := parsed.Format()
	require.NoError(t, err)
	gosum := "gotest.tools/gotestsum v1.13.0 h1:aaa=\ngotest.tools/gotestsum v1.13.0/go.mod h1:bbb=\n"
	require.NoError(t, os.WriteFile("go.mod", gomod, 0644))
	require.NoError(t, os.WriteFile("go.sum", []byte(gosum), 0644))
	require.NoError(t, os.WriteFile("main.go", []byte("package main\n"), 0644))
	runVanityTestGit(t, "init", "-q")
	runVanityTestGit(t, "add", ".")
	runVanityTestGit(t, "-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-q", "-m", "init")

	t.Setenv("CI", "true")

	oldChecker := vanityHostChecker
	vanityHostChecker = func(string) bool { return false }
	defer func() { vanityHostChecker = oldChecker }()
	oldResolver := vanityVCSResolver
	vanityVCSResolver = func(modulePath, _ string) (string, string, error) {
		return "https://github.com/gotestyourself/gotestsum", modulePath, nil
	}
	defer func() { vanityVCSResolver = oldResolver }()
	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	// No vanity state degrades to the plain check: the committed tree is clean.
	require.NoError(t, checkDirtyInCIWithVanityRestored(nil))

	state, err := injectVanityReplaces()
	require.NoError(t, err)
	require.NotNil(t, state)

	// Simulate go mod tidy rewriting go.sum onto the mirror path while the replace is active.
	tidiedGoSum := "github.com/gotestyourself/gotestsum v1.13.0 h1:xxx=\n"
	require.NoError(t, os.WriteFile("go.sum", []byte(tidiedGoSum), 0644))
	activeGoMod, err := os.ReadFile("go.mod")
	require.NoError(t, err)
	require.Contains(t, string(activeGoMod), "replace gotest.tools/gotestsum")

	// The defect this pins: the PLAIN check sees the transient injected state as dirt.
	require.Error(t, checkDirtyInCI())

	// The restore-aware check passes: go.mod/go.sum differ from HEAD only by the transient vanity mutation.
	require.NoError(t, checkDirtyInCIWithVanityRestored(state))

	// The active mirror state must survive the check for the test and build phases.
	afterGoMod, err := os.ReadFile("go.mod")
	require.NoError(t, err)
	assert.Equal(t, string(activeGoMod), string(afterGoMod))
	afterGoSum, err := os.ReadFile("go.sum")
	require.NoError(t, err)
	assert.Equal(t, tidiedGoSum, string(afterGoSum))

	// Real dirt in an unrelated file still fails while vanity is active.
	require.NoError(t, os.WriteFile("main.go", []byte("package main // edited\n"), 0644))
	assert.Error(t, checkDirtyInCIWithVanityRestored(state))
	runVanityTestGit(t, "checkout", "-q", "--", "main.go")

	// A go.mod change beyond the injected replaces survives the restore and still fails.
	withExtra := append([]byte{}, activeGoMod...)
	withExtra = append(withExtra, []byte("\nrequire example.com/extra v1.0.0\n")...)
	require.NoError(t, os.WriteFile("go.mod", withExtra, 0644))
	assert.Error(t, checkDirtyInCIWithVanityRestored(state))
	require.NoError(t, os.WriteFile("go.mod", activeGoMod, 0644))

	// The deferred cleanup then leaves the committed tree byte-identical.
	require.NoError(t, removeVanityReplaces(state))
	out, err := exec.Command("git", "status", "--porcelain").Output()
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(string(out)))
}
