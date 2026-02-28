package build

import (
	"os"

	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestFindMainPackagesParsesOutput(t *testing.T) {
	mock := runner.NewMock()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("example.com/cmd/foo\n\nexample.com/cmd/bar\n"), nil)

	pkgs, err := findMainPackages(mock)
	require.Nil(t, err)
	require.Equal(t, 2, len(pkgs))
	assert.False(t, pkgs[0] != "example.com/cmd/foo" || pkgs[1] != "example.com/cmd/bar")
}

func TestFindMainPackagesEmpty(t *testing.T) {
	mock := runner.NewMock()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("\n\n"), nil)

	pkgs, err := findMainPackages(mock)
	require.Nil(t, err)
	assert.Equal(t, 0, len(pkgs))
}

func TestBinaryNameFromImportPath(t *testing.T) {
	tests := []struct {
		pkg, module, want string
	}{
		// Single level below module → use module name
		{"github.com/wow-look-at-my/go-toolchain/src", "github.com/wow-look-at-my/go-toolchain", "go-toolchain"},
		{"example.com/src", "example.com", "example.com"},
		{"mymod/app", "mymod", "mymod"},

		// Deeper path → use leaf directory
		{"example.com/cmd/foo", "example.com", "foo"},
		{"example.com/cmd/bar", "example.com", "bar"},
		{"example.com/tools/linter", "example.com", "linter"},

		// No module prefix match → fallback to basename
		{"unrelated/pkg", "example.com", "pkg"},

		// Empty module name → fallback to basename
		{"example.com/cmd/foo", "", "foo"},
	}
	for _, tt := range tests {
		got := binaryNameFromImportPath(tt.pkg, tt.module)
		assert.Equal(t, tt.want, got)
	}
}

func TestResolveBuildTargetsGoFilesInRoot(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := runner.NewMock()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("example.com\n"), nil)
	mock.SetResponse("go", []string{"list", "-m"},
		[]byte("example.com\n"), nil)

	targets, err := ResolveBuildTargets(mock)
	require.Nil(t, err)
	require.Equal(t, 1, len(targets))
	assert.Equal(t, "example.com", targets[0].ImportPath)
	assert.Equal(t, "example.com", targets[0].OutputName)
}

func TestResolveBuildTargetsAutoDetectSingle(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := runner.NewMock()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("example.com/cmd/myapp\n"), nil)
	mock.SetResponse("go", []string{"list", "-m"},
		[]byte("example.com\n"), nil)

	targets, err := ResolveBuildTargets(mock)
	require.Nil(t, err)
	assert.False(t, len(targets) != 1 || targets[0].ImportPath != "example.com/cmd/myapp" || targets[0].OutputName != "myapp")
}

func TestResolveBuildTargetsAutoDetectMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := runner.NewMock()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("example.com/cmd/foo\nexample.com/cmd/bar\n"), nil)
	mock.SetResponse("go", []string{"list", "-m"},
		[]byte("example.com\n"), nil)

	targets, err := ResolveBuildTargets(mock)
	require.Nil(t, err)
	require.Equal(t, 2, len(targets))
	assert.Equal(t, "bar", targets[0].OutputName)
	assert.Equal(t, "foo", targets[1].OutputName)
}

func TestResolveBuildTargetsAutoDetectSrcDir(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := runner.NewMock()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("github.com/wow-look-at-my/go-toolchain/src\n"), nil)
	mock.SetResponse("go", []string{"list", "-m"},
		[]byte("github.com/wow-look-at-my/go-toolchain\n"), nil)

	targets, err := ResolveBuildTargets(mock)
	require.Nil(t, err)
	require.Equal(t, 1, len(targets))
	// Binary should be named after the module, not "src"
	assert.Equal(t, "go-toolchain", targets[0].OutputName)
}
