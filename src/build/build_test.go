package build

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindMainPackagesParsesOutput(t *testing.T) {
	mock := NewMockRunner()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("example.com/cmd/foo\n\nexample.com/cmd/bar\n"), nil)

	pkgs, err := findMainPackages(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pkgs) != 2 {
		t.Fatalf("expected 2 packages, got %d", len(pkgs))
	}
	if pkgs[0] != "example.com/cmd/foo" || pkgs[1] != "example.com/cmd/bar" {
		t.Errorf("unexpected packages: %v", pkgs)
	}
}

func TestFindMainPackagesEmpty(t *testing.T) {
	mock := NewMockRunner()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("\n\n"), nil)

	pkgs, err := findMainPackages(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pkgs) != 0 {
		t.Errorf("expected 0 packages, got %d", len(pkgs))
	}
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
		if got != tt.want {
			t.Errorf("binaryNameFromImportPath(%q, %q) = %q, want %q", tt.pkg, tt.module, got, tt.want)
		}
	}
}

func TestResolveBuildTargetsExplicitSrc(t *testing.T) {
	targets, err := ResolveBuildTargets(nil, "./cmd/myapp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].ImportPath != "./cmd/myapp" || targets[0].OutputName != "myapp" {
		t.Errorf("unexpected target: %+v", targets[0])
	}
}

func TestResolveBuildTargetsGoFilesInRoot(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a .go file in root
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	targets, err := ResolveBuildTargets(nil, ".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 || targets[0].ImportPath != "." || targets[0].OutputName != filepath.Base(tmpDir) {
		t.Errorf("unexpected target: %+v", targets)
	}
}

func TestResolveBuildTargetsAutoDetectSingle(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := NewMockRunner()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("example.com/cmd/myapp\n"), nil)
	mock.SetResponse("go", []string{"list", "-m"},
		[]byte("example.com\n"), nil)

	targets, err := ResolveBuildTargets(mock, ".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 || targets[0].ImportPath != "example.com/cmd/myapp" || targets[0].OutputName != "myapp" {
		t.Errorf("unexpected target: %+v", targets)
	}
}

func TestResolveBuildTargetsAutoDetectMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := NewMockRunner()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("example.com/cmd/foo\nexample.com/cmd/bar\n"), nil)
	mock.SetResponse("go", []string{"list", "-m"},
		[]byte("example.com\n"), nil)

	targets, err := ResolveBuildTargets(mock, ".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0].OutputName != "foo" || targets[1].OutputName != "bar" {
		t.Errorf("unexpected names: %q, %q", targets[0].OutputName, targets[1].OutputName)
	}
}

func TestResolveBuildTargetsAutoDetectSrcDir(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := NewMockRunner()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("github.com/wow-look-at-my/go-toolchain/src\n"), nil)
	mock.SetResponse("go", []string{"list", "-m"},
		[]byte("github.com/wow-look-at-my/go-toolchain\n"), nil)

	targets, err := ResolveBuildTargets(mock, ".")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	// Binary should be named after the module, not "src"
	if targets[0].OutputName != "go-toolchain" {
		t.Errorf("expected output name 'go-toolchain', got %q", targets[0].OutputName)
	}
}
