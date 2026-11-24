package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stampProbe is a program that prints its stamp variables: some of package main, and one of a package it imports.
var stampProbe = map[string]string{
	"go.mod": "module example.com/stampprobe\n\ngo 1.21\n",
	"cmd/probe/main.go": `package main

import (
	"fmt"

	"example.com/stampprobe/lib"
)

var (
	Version = "dev"
	gitHash string
)

func main() {
	fmt.Printf("main.Version=%s\nmain.gitHash=%s\nlib.Version=%s\n", Version, gitHash, lib.Version)
}
`,
	"lib/lib.go": "package lib\n\nvar Version = \"dev\"\n",
}

// The flags are only right when a real link fills the variables. The linker skips an -X that names no symbol. It
// gives no message, so a test of the flag text alone cannot see a wrong package name.
func TestTheStampsFillTheVariablesOfARealProgram(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	for name, body := range stampProbe {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	t.Chdir(dir)
	t.Setenv(gosmoReleaseEnv, "53")
	t.Setenv(gosmoReleaseRevisionEnv, "f870077")
	t.Setenv(RevisionEnv, "abc123")
	t.Setenv("GOFLAGS", "")
	t.Setenv("CGO_ENABLED", "0")

	out := filepath.Join(dir, "probe")
	build := exec.Command("go", "build", "-o", out, "-ldflags", jobLDFlags("example.com/stampprobe/cmd/probe", ""), "./cmd/probe")
	built, err := build.CombinedOutput()
	require.NoError(t, err, "go build: %s", built)

	printed, err := exec.Command(out).Output()
	require.NoError(t, err)
	assert.Equal(t, "main.Version=53 (f870077)\nmain.gitHash=abc123\nlib.Version=53 (f870077)\n", string(printed))
}
