package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestParseVanityModulesFromSum(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	gosum := `github.com/spf13/cobra v1.10.2 h1:abc123=
github.com/spf13/cobra v1.10.2/go.mod h1:def456=
gotest.tools/gotestsum v1.13.0 h1:aaa=
gotest.tools/gotestsum v1.13.0/go.mod h1:bbb=
modernc.org/sqlite v1.45.0 h1:ccc=
modernc.org/sqlite v1.45.0/go.mod h1:ddd=
dario.cat/mergo v1.0.0 h1:eee=
golang.org/x/mod v0.30.0 h1:fff=
gopkg.in/yaml.v3 v3.0.1 h1:ggg=
`
	os.WriteFile("go.sum", []byte(gosum), 0644)

	modules, err := parseVanityModulesFromSum()
	require.Nil(t, err)

	// Should include vanity hosts: gotest.tools, modernc.org, dario.cat
	// Should exclude: github.com, golang.org, gopkg.in
	assert.Equal(t, 3, len(modules))

	hosts := map[string]bool{}
	for _, m := range modules {
		hosts[m.Host] = true
	}
	assert.True(t, hosts["gotest.tools"])
	assert.True(t, hosts["modernc.org"])
	assert.True(t, hosts["dario.cat"])
}

func TestParseVanityModulesFromSumNoFile(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	modules, err := parseVanityModulesFromSum()
	assert.NotNil(t, err)
	assert.True(t, os.IsNotExist(err))
	assert.Nil(t, modules)
}

func TestParseVanityModulesFromSumDedup(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// Same module appears in both hash and go.mod hash lines
	gosum := `gotest.tools/gotestsum v1.13.0 h1:aaa=
gotest.tools/gotestsum v1.13.0/go.mod h1:bbb=
`
	os.WriteFile("go.sum", []byte(gosum), 0644)

	modules, err := parseVanityModulesFromSum()
	require.Nil(t, err)
	assert.Equal(t, 1, len(modules))
	assert.Equal(t, "gotest.tools/gotestsum", modules[0].Path)
	assert.Equal(t, "v1.13.0", modules[0].Version)
}

func TestVcsURLToModulePath(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://github.com/gotestyourself/gotestsum", "github.com/gotestyourself/gotestsum"},
		{"https://github.com/foo/bar.git", "github.com/foo/bar"},
		{"http://github.com/foo/bar", "github.com/foo/bar"},
		{"github.com/foo/bar", "github.com/foo/bar"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, vcsURLToModulePath(tc.url))
	}
}

func TestParseGoImportMeta(t *testing.T) {
	html := `<!DOCTYPE html>
<html><head>
<meta name="go-import" content="gotest.tools/gotestsum git https://github.com/gotestyourself/gotestsum">
</head></html>`

	url, prefix, err := parseGoImportMeta(html, "gotest.tools/gotestsum")
	require.Nil(t, err)
	assert.Equal(t, "https://github.com/gotestyourself/gotestsum", url)
	assert.Equal(t, "gotest.tools/gotestsum", prefix)
}

func TestParseGoImportMetaPrefixMatch(t *testing.T) {
	// Module path is longer than the prefix in the meta tag
	html := `<meta name="go-import" content="gotest.tools git https://github.com/gotestyourself/gotest.tools">`

	url, prefix, err := parseGoImportMeta(html, "gotest.tools/v3")
	require.Nil(t, err)
	assert.Equal(t, "https://github.com/gotestyourself/gotest.tools", url)
	assert.Equal(t, "gotest.tools", prefix)
}

func TestParseGoImportMetaNotFound(t *testing.T) {
	html := `<html><head><title>Nothing here</title></head></html>`
	_, _, err := parseGoImportMeta(html, "example.com/foo")
	assert.NotNil(t, err)
}

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

func TestRemoveVanityReplacesEmpty(t *testing.T) {
	err := removeVanityReplaces(nil)
	assert.Nil(t, err)
}

func TestWellKnownHostsExcluded(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	gosum := `github.com/foo/bar v1.0.0 h1:aaa=
gitlab.com/baz/qux v2.0.0 h1:bbb=
golang.org/x/mod v0.30.0 h1:ccc=
gopkg.in/yaml.v3 v3.0.1 h1:ddd=
bitbucket.org/test/repo v0.1.0 h1:eee=
`
	os.WriteFile("go.sum", []byte(gosum), 0644)

	modules, err := parseVanityModulesFromSum()
	require.Nil(t, err)
	assert.Equal(t, 0, len(modules))
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
