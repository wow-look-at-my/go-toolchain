package memlimit

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestInjectCreatesFile(t *testing.T) {
	dir := t.TempDir()

	created, err := Inject(dir)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if !created {
		t.Fatal("expected Inject to report the file was created")
	}

	got, err := os.ReadFile(filepath.Join(dir, GuardFileName))
	if err != nil {
		t.Fatalf("reading injected file: %v", err)
	}
	if string(got) != guardSource {
		t.Fatal("injected content does not match the embedded guard source")
	}
}

func TestInjectIdempotent(t *testing.T) {
	dir := t.TempDir()

	if _, err := Inject(dir); err != nil {
		t.Fatalf("first Inject: %v", err)
	}
	created, err := Inject(dir)
	if err != nil {
		t.Fatalf("second Inject: %v", err)
	}
	if created {
		t.Fatal("expected second Inject to be a no-op")
	}
}

func TestInjectOverwritesStale(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, GuardFileName)
	if err := os.WriteFile(target, []byte("package main\n// stale\n"), 0o644); err != nil {
		t.Fatalf("seeding stale file: %v", err)
	}

	created, err := Inject(dir)
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if !created {
		t.Fatal("expected Inject to overwrite the stale file")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("reading file: %v", err)
	}
	if string(got) != guardSource {
		t.Fatal("stale file was not refreshed to the current guard source")
	}
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
	if err != nil {
		t.Fatalf("InjectAll: %v", err)
	}

	sort.Strings(changed)
	want := []string{".", "cmd/tool"}
	if len(changed) != len(want) || changed[0] != want[0] || changed[1] != want[1] {
		t.Fatalf("changed dirs = %v, want %v", changed, want)
	}

	// Guard present in both main packages, absent from the library package.
	for _, dir := range []string{".", "cmd/tool"} {
		if _, err := os.Stat(filepath.Join(mod, dir, GuardFileName)); err != nil {
			t.Errorf("expected guard in %s: %v", dir, err)
		}
	}
	if _, err := os.Stat(filepath.Join(mod, "internal/lib", GuardFileName)); !os.IsNotExist(err) {
		t.Errorf("guard should not be injected into a non-main package (err=%v)", err)
	}

	// Second pass is a clean no-op.
	changed, err = InjectAll()
	if err != nil {
		t.Fatalf("second InjectAll: %v", err)
	}
	if len(changed) != 0 {
		t.Fatalf("expected second InjectAll to change nothing, got %v", changed)
	}
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func chdir(t *testing.T, dir string) func() {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	return func() { _ = os.Chdir(prev) }
}
