package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
)

func TestReleaseVarValueNamesTheReleaseAndItsCommit(t *testing.T) {
	t.Serial()
	assert.Equal(t, "53 (f870077)", releaseVarValue("53", "f870077"))
	assert.Equal(t, "", releaseVarValue("", "f870077"), "a build with no release keeps the program's placeholder")
	assert.Equal(t, "", releaseVarValue("53", ""))
}

func TestReleaseVarStampQuotesTheWholeFieldAndNamesOnlyDeclaredVariables(t *testing.T) {
	t.Serial()
	got := releaseVarStamp("example.com/m/cmd", set.Of("Version", "gitHash"), "53 (abc)")
	assert.Equal(t, "-X 'example.com/m/cmd.Version=53 (abc)'", got)
	assert.Equal(t, "", releaseVarStamp("example.com/m/cmd", set.Of("gitHash"), "53 (abc)"))
}

// writeModule lays out a module whose main package imports its own cmd package.
func writeModule(t *testing.T) string {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":         "module example.com/m\n\ngo 1.24\n",
		"main.go":        "package main\n\nimport (\n\t\"os\"\n\n\t\"example.com/m/cmd\"\n)\n\nfunc main() { os.Exit(cmd.Run()) }\n",
		"main_test.go":   "package main\n\nimport _ \"example.com/m/testonly\"\n",
		"cmd/root.go":    "package cmd\n\nvar Version = \"dev\"\n\nfunc Run() int { return 0 }\n",
		"testonly/t.go":  "package testonly\n\nvar Version = \"dev\"\n",
		"unused/u.go":    "package unused\n\nvar Version = \"dev\"\n",
		"cmd/version.go": "package cmd\n\nvar version string\n",
	}
	for name, body := range files {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	return root
}

func TestModuleImportsListsOnlyThisModulesPackagesFromNonTestFiles(t *testing.T) {
	t.Serial()
	root := writeModule(t)
	assert.Equal(t, []string{"example.com/m/cmd"}, moduleImports("example.com/m", root))
	assert.Empty(t, moduleImports("", root))
}

func TestReleaseVarLDFlagsFillsTheVersionOfAnImportedPackage(t *testing.T) {
	t.Serial()
	t.Chdir(writeModule(t))
	t.Setenv(gosmoReleaseEnv, "53")
	t.Setenv(gosmoReleaseRevisionEnv, "f870077")
	assert.Equal(t, "-X 'example.com/m/cmd.Version=53 (f870077)' -X 'example.com/m/cmd.version=53 (f870077)'",
		releaseVarLDFlags("example.com/m"),
		"the main package declares no version, the cmd package it imports declares two, and a package it does not import is left alone")
}

func TestReleaseVarLDFlagsIsEmptyWithoutARelease(t *testing.T) {
	t.Serial()
	t.Chdir(writeModule(t))
	t.Setenv(gosmoReleaseEnv, "")
	t.Setenv(gosmoReleaseRevisionEnv, "")
	assert.Equal(t, "", releaseVarLDFlags("example.com/m"))
}
