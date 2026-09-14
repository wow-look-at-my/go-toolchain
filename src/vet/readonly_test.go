package vet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readonlyModule writes a module whose package host declares a readonly var,
// and whose main package does what main says with it.
func readonlyModule(t *testing.T, main string) string {
	t.Helper()
	dir := t.TempDir()
	host := "package host\n\nreadonly var Name string = \"unknown\"\n\nfunc Set(n string) { Name = n }\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/ro\n\ngo 1.27\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "host"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "host", "host.go"), []byte(host), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(main), 0o644))
	return dir
}

// Vet parses and type-checks with the go/parser and go/types this binary links,
// and the pipeline runs only as a build of the active toolchain. So the fork's
// `readonly var` parses and type-checks.
func TestVetTypeChecksAReadonlyVarUnderTheFork(t *testing.T) {
	t.Serial()
	t.Chdir(readonlyModule(t, "package main\n\nimport \"example.test/ro/host\"\n\nfunc main() {\n\thost.Set(\"x\")\n\tprintln(host.Name)\n}\n"))

	_, err := RunOnPattern("./...", false, nil)
	assert.NoError(t, err)
}

// The fork's go/types reads a readonly var as a value outside its package, so
// vet rejects the assignment the compiler rejects.
func TestVetRejectsAssigningAReadonlyVarFromAnotherPackage(t *testing.T) {
	t.Serial()
	t.Chdir(readonlyModule(t, "package main\n\nimport \"example.test/ro/host\"\n\nfunc main() {\n\thost.Name = \"y\"\n}\n"))

	_, err := RunOnPattern("./...", false, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "readonly outside package host")
}
