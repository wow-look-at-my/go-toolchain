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

	t.Chdir(mod)

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

	// The repeat pass is a clean no-op.
	changed, err = InjectAll()
	require.NoError(t, err)
	require.Empty(t, changed)
}

func TestCleanupAllRemovesInjectedGuards(t *testing.T) {
	mod := t.TempDir()
	writeFile(t, mod, "go.mod", "module example.com/thing\n\ngo 1.19\n")
	writeFile(t, mod, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, mod, "cmd/tool/main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, mod, "internal/lib/lib.go", "package lib\n")

	t.Chdir(mod)

	_, err := InjectAll()
	require.NoError(t, err)

	removed, err := CleanupAll()
	require.NoError(t, err)
	sort.Strings(removed)
	require.Equal(t, []string{".", "cmd/tool"}, removed)

	// Guards are gone from every main package...
	for _, dir := range []string{".", "cmd/tool"} {
		_, statErr := os.Stat(filepath.Join(mod, dir, GuardFileName))
		require.Truef(t, os.IsNotExist(statErr), "guard should be removed from %s", dir)
	}
	// ...but the real source files are left untouched.
	for _, rel := range []string{"main.go", "cmd/tool/main.go", "internal/lib/lib.go"} {
		_, statErr := os.Stat(filepath.Join(mod, rel))
		require.NoErrorf(t, statErr, "cleanup must not remove %s", rel)
	}
}

func TestCleanupAllIdempotentWhenAbsent(t *testing.T) {
	mod := t.TempDir()
	writeFile(t, mod, "go.mod", "module example.com/thing\n\ngo 1.19\n")
	writeFile(t, mod, "main.go", "package main\n\nfunc main() {}\n")

	t.Chdir(mod)

	// No guards were ever injected: cleanup is a clean no-op, not an error.
	removed, err := CleanupAll()
	require.NoError(t, err)
	require.Empty(t, removed)
}

func TestCleanupAllNoModule(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	removed, err := CleanupAll()
	require.NoError(t, err)
	require.Empty(t, removed)
}

func TestInjectAllThenCleanupAllRoundTrip(t *testing.T) {
	mod := t.TempDir()
	writeFile(t, mod, "go.mod", "module example.com/thing\n\ngo 1.19\n")
	writeFile(t, mod, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, mod, "cmd/tool/main.go", "package main\n\nfunc main() {}\n")

	t.Chdir(mod)

	injected, err := InjectAll()
	require.NoError(t, err)
	sort.Strings(injected)

	removed, err := CleanupAll()
	require.NoError(t, err)
	sort.Strings(removed)

	// Whatever was injected is exactly what gets cleaned up.
	require.Equal(t, injected, removed)
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}
