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

	url, err := parseGoImportMeta(html, "gotest.tools/gotestsum")
	require.Nil(t, err)
	assert.Equal(t, "https://github.com/gotestyourself/gotestsum", url)
}

func TestParseGoImportMetaPrefixMatch(t *testing.T) {
	// Module path is longer than the prefix in the meta tag
	html := `<meta name="go-import" content="gotest.tools git https://github.com/gotestyourself/gotest.tools">`

	url, err := parseGoImportMeta(html, "gotest.tools/v3")
	require.Nil(t, err)
	assert.Equal(t, "https://github.com/gotestyourself/gotest.tools", url)
}

func TestParseGoImportMetaNotFound(t *testing.T) {
	html := `<html><head><title>Nothing here</title></head></html>`
	_, err := parseGoImportMeta(html, "example.com/foo")
	assert.NotNil(t, err)
}

func TestInjectVanityReplacesNoGoSum(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	replaces, err := injectVanityReplaces()
	assert.Nil(t, err)
	assert.Nil(t, replaces)
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

	replaces, err := injectVanityReplaces()
	assert.Nil(t, err)
	assert.Nil(t, replaces)
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
	vanityVCSResolver = func(modulePath, version string) (string, error) {
		if modulePath == "gotest.tools/gotestsum" {
			return "https://github.com/gotestyourself/gotestsum", nil
		}
		return "", os.ErrNotExist
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	// Inject
	replaces, err := injectVanityReplaces()
	require.Nil(t, err)
	require.Equal(t, 1, len(replaces))
	assert.Equal(t, "gotest.tools/gotestsum", replaces[0].OldPath)
	assert.Equal(t, "github.com/gotestyourself/gotestsum", replaces[0].NewPath)

	// Verify go.mod has the replace directive
	data, _ := os.ReadFile("go.mod")
	content := string(data)
	assert.Contains(t, content, "replace gotest.tools/gotestsum")
	assert.Contains(t, content, "github.com/gotestyourself/gotestsum")

	// Remove
	err = removeVanityReplaces(replaces)
	require.Nil(t, err)

	// Verify replace is gone
	data, _ = os.ReadFile("go.mod")
	content = string(data)
	assert.NotContains(t, content, "replace")
	// Original require should still be there
	assert.Contains(t, content, "gotest.tools/gotestsum")
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
	vanityVCSResolver = func(modulePath, version string) (string, error) {
		mapping := map[string]string{
			"modernc.org/sqlite": "https://gitlab.com/cznic/sqlite",
			"modernc.org/libc":   "https://gitlab.com/cznic/libc",
		}
		if url, ok := mapping[modulePath]; ok {
			return url, nil
		}
		return "", os.ErrNotExist
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	replaces, err := injectVanityReplaces()
	require.Nil(t, err)
	assert.Equal(t, 2, len(replaces))

	// Verify both replaces are in go.mod
	data, _ := os.ReadFile("go.mod")
	content := string(data)
	assert.Contains(t, content, "gitlab.com/cznic/sqlite")
	assert.Contains(t, content, "gitlab.com/cznic/libc")

	// Clean up
	err = removeVanityReplaces(replaces)
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
	vanityVCSResolver = func(modulePath, version string) (string, error) {
		return "", os.ErrNotExist
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	replaces, err := injectVanityReplaces()
	assert.Nil(t, err)
	assert.Nil(t, replaces)

	// go.mod should be unchanged
	data, _ := os.ReadFile("go.mod")
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
	vanityVCSResolver = func(modulePath, version string) (string, error) {
		return "https://github.com/gotestyourself/gotestsum", nil
	}
	defer func() { vanityVCSResolver = oldResolver }()

	oldJSON := jsonOutput
	jsonOutput = true
	defer func() { jsonOutput = oldJSON }()

	replaces, err := injectVanityReplaces()
	require.Nil(t, err)
	require.Equal(t, 1, len(replaces))

	// Existing replace should still be there
	data, _ := os.ReadFile("go.mod")
	content := string(data)
	assert.Contains(t, content, "example.com/existing")

	// Remove only injected replaces
	err = removeVanityReplaces(replaces)
	require.Nil(t, err)

	data, _ = os.ReadFile("go.mod")
	content = string(data)
	// Existing replace preserved, vanity replace removed
	assert.Contains(t, content, "example.com/existing")
	assert.NotContains(t, content, "gotestyourself")
}
