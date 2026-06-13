package memlimit

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInjectCreatesFile(t *testing.T) {
	dir := t.TempDir()

	created, err := Inject(dir)
	require.NoError(t, err)
	require.True(t, created)

	got, err := os.ReadFile(filepath.Join(dir, GuardFileName))
	require.NoError(t, err)
	require.Equal(t, guardSource, string(got))
}

func TestInjectIdempotent(t *testing.T) {
	dir := t.TempDir()

	_, err := Inject(dir)
	require.NoError(t, err)

	created, err := Inject(dir)
	require.NoError(t, err)
	require.False(t, created)
}

func TestInjectOverwritesStale(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, GuardFileName)
	require.NoError(t, os.WriteFile(target, []byte("package main\n// stale\n"), 0o644))

	created, err := Inject(dir)
	require.NoError(t, err)
	require.True(t, created)

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, guardSource, string(got))
}

func TestInjectAllDiscoversMainPackages(t *testing.T) {
	mod := t.TempDir()
	writeFile(t, mod, "go.mod", "module example.com/thing\n\ngo 1.19\n")
	writeFile(t, mod, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, mod, "cmd/tool/main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, mod, "internal/lib/lib.go", "package lib\n")

	restore := chdir(t, mod)
	defer restore()

	changed, err := InjectAll()
	require.NoError(t, err)

	sort.Strings(changed)
	require.Equal(t, []string{".", "cmd/tool"}, changed)

	// Guard present in both main packages, absent from the library package.
	for _, dir := range []string{".", "cmd/tool"} {
		_, statErr := os.Stat(filepath.Join(mod, dir, GuardFileName))
		require.NoErrorf(t, statErr, "expected guard in %s", dir)
	}
	_, statErr := os.Stat(filepath.Join(mod, "internal/lib", GuardFileName))
	require.True(t, os.IsNotExist(statErr), "guard should not be injected into a non-main package")

	// Second pass is a clean no-op.
	changed, err = InjectAll()
	require.NoError(t, err)
	require.Empty(t, changed)
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	return func() { _ = os.Chdir(prev) }
}
