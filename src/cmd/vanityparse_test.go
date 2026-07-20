package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
google.golang.org/genproto/googleapis/api v0.0.0-20260401024825-9d38bb4040a9 h1:fff=
google.golang.org/grpc v1.80.0 h1:ggg=
`
	os.WriteFile("go.sum", []byte(gosum), 0644)

	// google.golang.org is a well-known host: its modules (genproto, grpc,
	// protobuf, ...) always resolve via the Go proxy, so they must never be
	// treated as rewritable vanity modules. Treating them as vanity caused a
	// stale build to mis-rewrite them onto GitHub mirrors when a slow network
	// made the reachability probe time out.
	modules, err := parseVanityModulesFromSum()
	require.Nil(t, err)
	assert.Equal(t, 0, len(modules))
}
