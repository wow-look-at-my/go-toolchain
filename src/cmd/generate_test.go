package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDirectives(t *testing.T) {
	// Create a temp directory with a test file
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")

	// Use string concatenation to avoid these being picked up as real directives
	content := "package main\n\n" +
		"//go:generate echo hello\n" +
		"//go:generate sh -c \"echo world\"\n\n" +
		"func main() {}\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	directives, err := parseDirectives(testFile)
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 2 {
		t.Fatalf("expected 2 directives, got %d", len(directives))
	}

	if directives[0].Command != "echo hello" {
		t.Errorf("expected 'echo hello', got %q", directives[0].Command)
	}
	if directives[0].Line != 3 {
		t.Errorf("expected line 3, got %d", directives[0].Line)
	}

	if directives[1].Command != `sh -c "echo world"` {
		t.Errorf("expected 'sh -c \"echo world\"', got %q", directives[1].Command)
	}
	if directives[1].Line != 4 {
		t.Errorf("expected line 4, got %d", directives[1].Line)
	}
}

func TestParseDirectivesNoDirectives(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")

	content := `package main

func main() {}
`
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	directives, err := parseDirectives(testFile)
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 0 {
		t.Fatalf("expected 0 directives, got %d", len(directives))
	}
}

func TestFindGenerateDirectives(t *testing.T) {
	dir := t.TempDir()

	// Create subdirectory
	subdir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}

	// File in root
	file1 := filepath.Join(dir, "main.go")
	if err := os.WriteFile(file1, []byte("package main\n//go:generate echo root\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// File in subdir
	file2 := filepath.Join(subdir, "sub.go")
	if err := os.WriteFile(file2, []byte("package sub\n//go:generate echo sub\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Non-go file (should be ignored)
	file3 := filepath.Join(dir, "readme.txt")
	if err := os.WriteFile(file3, []byte("//go:generate echo ignored\n"), 0644); err != nil {
		t.Fatal(err)
	}

	directives, err := findGenerateDirectives(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 2 {
		t.Fatalf("expected 2 directives, got %d", len(directives))
	}
}

func TestFindGenerateDirectivesSkipsVendor(t *testing.T) {
	dir := t.TempDir()

	// Create vendor directory
	vendor := filepath.Join(dir, "vendor")
	if err := os.Mkdir(vendor, 0755); err != nil {
		t.Fatal(err)
	}

	// File in vendor (should be ignored)
	vendorFile := filepath.Join(vendor, "vendor.go")
	if err := os.WriteFile(vendorFile, []byte("package vendor\n//go:generate echo vendor\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// File in root
	mainFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(mainFile, []byte("package main\n//go:generate echo main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	directives, err := findGenerateDirectives(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(directives) != 1 {
		t.Fatalf("expected 1 directive (vendor should be skipped), got %d", len(directives))
	}

	if directives[0].Command != "echo main" {
		t.Errorf("expected 'echo main', got %q", directives[0].Command)
	}
}

func TestExecuteDirectiveSuccess(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	d := generateDirective{
		File:    testFile,
		Line:    1,
		Command: "echo success",
	}

	err := executeDirective(d, true) // quiet mode to avoid stdout pollution
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
}

func TestExecuteDirectiveFailure(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	d := generateDirective{
		File:    testFile,
		Line:    1,
		Command: "exit 1",
	}

	err := executeDirective(d, true)
	if err == nil {
		t.Fatal("expected error for failing command")
	}
	if !strings.Contains(err.Error(), "generate failed") {
		t.Errorf("expected 'generate failed' in error, got: %v", err)
	}
}

func TestPrefixOutput(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "empty",
			input:  "",
			expect: "",
		},
		{
			name:   "single line",
			input:  "hello\n",
			expect: "\t> hello\n",
		},
		{
			name:   "multiple lines",
			input:  "line1\nline2\nline3\n",
			expect: "\t> line1\n\t> line2\n\t> line3\n",
		},
		{
			name:   "no trailing newline",
			input:  "hello",
			expect: "\t> hello\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := prefixOutput(tt.input)
			if got != tt.expect {
				t.Errorf("prefixOutput(%q) = %q, want %q", tt.input, got, tt.expect)
			}
		})
	}
}

func TestGuessPackage(t *testing.T) {
	tests := []struct {
		path   string
		expect string
	}{
		{"foo/bar/baz.go", "bar"},
		{"cmd/main.go", "cmd"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := guessPackage(tt.path)
			if got != tt.expect {
				t.Errorf("guessPackage(%q) = %q, want %q", tt.path, got, tt.expect)
			}
		})
	}
}

func TestRunGenerateWithHash(t *testing.T) {
	// Save current directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	// Create temp directory with a generate directive
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(dir, "main.go")
	outputFile := filepath.Join(dir, "generated.txt")

	content := "package main\n" +
		"//go:generate sh -c \"echo generated > generated.txt\"\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// First, get the hash by finding directives
	directives, err := findGenerateDirectives(".")
	if err != nil {
		t.Fatal(err)
	}
	hash := computeDirectivesHash(directives)

	// Without hash, command should NOT run and should return error
	err = runGenerate(true, "")
	if err == nil {
		t.Fatal("runGenerate without hash should return error")
	}
	if _, err := os.Stat(outputFile); !os.IsNotExist(err) {
		t.Error("expected generated.txt to NOT be created without hash")
	}

	// With correct hash, command should run
	err = runGenerate(true, hash)
	if err != nil {
		t.Fatalf("runGenerate with hash failed: %v", err)
	}
	if _, err := os.Stat(outputFile); os.IsNotExist(err) {
		t.Error("expected generated.txt to be created with correct hash")
	}
}

func TestRunGenerateWrongHash(t *testing.T) {
	// Save current directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	// Create temp directory with a generate directive
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(dir, "main.go")
	outputFile := filepath.Join(dir, "generated.txt")

	content := "package main\n" +
		"//go:generate sh -c \"echo generated > generated.txt\"\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// With wrong hash, command should NOT run and should return error
	err = runGenerate(true, "wronghash123")
	if err == nil {
		t.Fatal("runGenerate with wrong hash should return error")
	}
	if _, err := os.Stat(outputFile); !os.IsNotExist(err) {
		t.Error("expected generated.txt to NOT be created with wrong hash")
	}
}

func TestRunGenerateSkip(t *testing.T) {
	// Save current directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	// Create temp directory with a generate directive
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(dir, "main.go")
	outputFile := filepath.Join(dir, "generated.txt")

	content := "package main\n" +
		"//go:generate sh -c \"echo generated > generated.txt\"\n"
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	// With "skip", command should NOT run but should succeed
	err = runGenerate(true, "skip")
	if err != nil {
		t.Fatalf("runGenerate with skip should succeed, got: %v", err)
	}
	if _, err := os.Stat(outputFile); !os.IsNotExist(err) {
		t.Error("expected generated.txt to NOT be created with skip")
	}
}

func TestRunGenerateNoDirectives(t *testing.T) {
	// Save current directory
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	// Create temp directory with no generate directives
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	testFile := filepath.Join(dir, "main.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatal(err)
	}

	err = runGenerate(true, "")
	if err != nil {
		t.Fatalf("runGenerate with no directives should succeed, got: %v", err)
	}
}

func TestComputeDirectivesHash(t *testing.T) {
	directives := []generateDirective{
		{File: "a.go", Line: 1, Command: "echo a"},
		{File: "b.go", Line: 2, Command: "echo b"},
	}

	hash1 := computeDirectivesHash(directives)
	if len(hash1) != 12 {
		t.Errorf("expected 12 char hash, got %d chars: %s", len(hash1), hash1)
	}

	// Same directives in different order should produce same hash
	reversed := []generateDirective{
		{File: "b.go", Line: 2, Command: "echo b"},
		{File: "a.go", Line: 1, Command: "echo a"},
	}
	hash2 := computeDirectivesHash(reversed)
	if hash1 != hash2 {
		t.Errorf("hash should be stable regardless of order: %s != %s", hash1, hash2)
	}

	// Different directives should produce different hash
	different := []generateDirective{
		{File: "a.go", Line: 1, Command: "echo different"},
	}
	hash3 := computeDirectivesHash(different)
	if hash1 == hash3 {
		t.Errorf("different directives should produce different hash")
	}
}
