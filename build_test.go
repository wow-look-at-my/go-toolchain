package main

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

func TestResolveBuildTargetsExplicitSrc(t *testing.T) {
	oldSrc, oldOut := srcPath, output
	defer func() { srcPath, output = oldSrc, oldOut }()

	srcPath = "./cmd/myapp"
	output = "mybin"

	targets, err := resolveBuildTargets(nil) // runner not needed for explicit path
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0].importPath != "./cmd/myapp" || targets[0].outputName != "mybin" {
		t.Errorf("unexpected target: %+v", targets[0])
	}
}

func TestResolveBuildTargetsExplicitSrcDefaultName(t *testing.T) {
	oldSrc, oldOut := srcPath, output
	defer func() { srcPath, output = oldSrc, oldOut }()

	srcPath = "./cmd/myapp"
	output = ""

	targets, err := resolveBuildTargets(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if targets[0].outputName != "myapp" {
		t.Errorf("expected output name 'myapp', got %q", targets[0].outputName)
	}
}

func TestResolveBuildTargetsGoFilesInRoot(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create a .go file in root
	os.WriteFile("main.go", []byte("package main\n"), 0644)

	oldSrc, oldOut := srcPath, output
	defer func() { srcPath, output = oldSrc, oldOut }()

	srcPath = "."
	output = "mybin"

	targets, err := resolveBuildTargets(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 || targets[0].importPath != "." || targets[0].outputName != "mybin" {
		t.Errorf("unexpected target: %+v", targets)
	}
}

func TestResolveBuildTargetsGoFilesInRootDefaultName(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("main.go", []byte("package main\n"), 0644)

	oldSrc, oldOut := srcPath, output
	defer func() { srcPath, output = oldSrc, oldOut }()

	srcPath = "."
	output = ""

	targets, err := resolveBuildTargets(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if targets[0].outputName != filepath.Base(tmpDir) {
		t.Errorf("expected output name %q, got %q", filepath.Base(tmpDir), targets[0].outputName)
	}
}

func TestResolveBuildTargetsAutoDetectSingle(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// No .go files in root
	oldSrc, oldOut := srcPath, output
	defer func() { srcPath, output = oldSrc, oldOut }()

	srcPath = "."
	output = "custom"

	mock := NewMockRunner()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("example.com/cmd/myapp\n"), nil)

	targets, err := resolveBuildTargets(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 1 || targets[0].importPath != "example.com/cmd/myapp" || targets[0].outputName != "custom" {
		t.Errorf("unexpected target: %+v", targets)
	}
}

func TestResolveBuildTargetsAutoDetectMultiple(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	oldSrc, oldOut := srcPath, output
	defer func() { srcPath, output = oldSrc, oldOut }()

	srcPath = "."
	output = ""

	mock := NewMockRunner()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("example.com/cmd/foo\nexample.com/cmd/bar\n"), nil)

	targets, err := resolveBuildTargets(mock)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0].outputName != "foo" || targets[1].outputName != "bar" {
		t.Errorf("unexpected names: %q, %q", targets[0].outputName, targets[1].outputName)
	}
}

func TestResolveBuildTargetsMultipleWithOutputFlag(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	oldSrc, oldOut := srcPath, output
	defer func() { srcPath, output = oldSrc, oldOut }()

	srcPath = "."
	output = "conflict"

	mock := NewMockRunner()
	mock.SetResponse("go", []string{"list", "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, "./..."},
		[]byte("example.com/cmd/foo\nexample.com/cmd/bar\n"), nil)

	_, err := resolveBuildTargets(mock)
	if err == nil {
		t.Fatal("expected error when -o used with multiple main packages")
	}
}
