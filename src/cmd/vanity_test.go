package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInjectVanityReplacesNoGoSum(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	state, err := injectVanityReplaces()
	assert.Nil(t, err)
	assert.Nil(t, state)
}

func TestInjectVanityReplacesAllReachable(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

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
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

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

	// Corrupt go.sum to simulate what go mod tidy would do while the
	// replace is active (swap vanity entries for replacement entries).
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
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

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
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

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
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

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
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

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
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	gomod := "module test\n\ngo 1.21\n\nrequire vanity.test/widget v1.2.3\n"
	os.WriteFile("go.mod", []byte(gomod), 0644)
	gosum := "vanity.test/widget v1.2.3 h1:aaa=\n"
	os.WriteFile("go.sum", []byte(gosum), 0644)

	oldChecker := vanityHostChecker
	vanityHostChecker = func(host string) bool { return false }
	defer func() { vanityHostChecker = oldChecker }()

	// This vanity module's real repository is on go.googlesource.com, which is
	// not a direct code host. Rewriting onto it would only swap one indirect
	// host for another, so the replace must be skipped entirely. (google.golang.org
	// modules can no longer reach this path — they are well-known and excluded
	// before the reachability check.)
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
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

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
