package cmd

import (
	"os"
	"path/filepath"
	
	"testing"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"

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
	require.NoError(t, os.WriteFile(testFile, []byte(content), 0644))

	directives, err := parseDirectives(testFile)
	require.Nil(t, err)

	require.Equal(t, 2, len(directives))

	assert.Equal(t, "echo hello", directives[0].Command)
	assert.Equal(t, 3, directives[0].Line)

	assert.Equal(t, `sh -c "echo world"`, directives[1].Command)
	assert.Equal(t, 4, directives[1].Line)
}

func TestParseDirectivesNoDirectives(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")

	content := `package main

func main() {}
`
	require.NoError(t, os.WriteFile(testFile, []byte(content), 0644))

	directives, err := parseDirectives(testFile)
	require.Nil(t, err)

	require.Equal(t, 0, len(directives))
}

func TestFindGenerateDirectives(t *testing.T) {
	dir := t.TempDir()

	// Create subdirectory
	subdir := filepath.Join(dir, "sub")
	require.NoError(t, os.Mkdir(subdir, 0755))

	// File in root
	file1 := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(file1, []byte("package main\n//go:generate echo root\n"), 0644))

	// File in subdir
	file2 := filepath.Join(subdir, "sub.go")
	require.NoError(t, os.WriteFile(file2, []byte("package sub\n//go:generate echo sub\n"), 0644))

	// Non-go file (should be ignored)
	file3 := filepath.Join(dir, "readme.txt")
	require.NoError(t, os.WriteFile(file3, []byte("//go:generate echo ignored\n"), 0644))

	directives, err := findGenerateDirectives(dir)
	require.Nil(t, err)

	require.Equal(t, 2, len(directives))
}

func TestFindGenerateDirectivesSkipsVendor(t *testing.T) {
	dir := t.TempDir()

	// Create vendor directory
	vendor := filepath.Join(dir, "vendor")
	require.NoError(t, os.Mkdir(vendor, 0755))

	// File in vendor (should be ignored)
	vendorFile := filepath.Join(vendor, "vendor.go")
	require.NoError(t, os.WriteFile(vendorFile, []byte("package vendor\n//go:generate echo vendor\n"), 0644))

	// File in root
	mainFile := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(mainFile, []byte("package main\n//go:generate echo main\n"), 0644))

	directives, err := findGenerateDirectives(dir)
	require.Nil(t, err)

	require.Equal(t, 1, len(directives))

	assert.Equal(t, "echo main", directives[0].Command)
}

func TestExecuteDirectiveSuccess(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package main\n"), 0644))

	d := generateDirective{
		File:    testFile,
		Line:    1,
		Command: "echo success",
	}

	err := executeDirective(d, true) // quiet mode to avoid stdout pollution
	require.Nil(t, err)
}

func TestExecuteDirectiveFailure(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package main\n"), 0644))

	d := generateDirective{
		File:    testFile,
		Line:    1,
		Command: "exit 1",
	}

	err := executeDirective(d, true)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "generate failed")
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
			assert.Equal(t, tt.expect, got)
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
			assert.Equal(t, tt.expect, got)
		})
	}
}

func TestRunGenerateWithHash(t *testing.T) {
	// Save current directory
	origDir, err := os.Getwd()
	require.Nil(t, err)
	defer os.Chdir(origDir)

	// Create temp directory with a generate directive
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	testFile := filepath.Join(dir, "main.go")
	outputFile := filepath.Join(dir, "generated.txt")

	content := "package main\n" +
		"//go:generate sh -c \"echo generated > generated.txt\"\n"
	require.NoError(t, os.WriteFile(testFile, []byte(content), 0644))

	// First, get the hash by finding directives
	directives, err := findGenerateDirectives(".")
	require.Nil(t, err)
	hash := computeDirectivesHash(directives)

	// Without hash, command should NOT run and should return error
	err = runGenerate(true, "")
	require.NotNil(t, err)
	_, err = os.Stat(outputFile)
	assert.True(t, os.IsNotExist(err))

	// With correct hash, command should run
	err = runGenerate(true, hash)
	require.Nil(t, err)
	_, err = os.Stat(outputFile)
	assert.False(t, os.IsNotExist(err))
}

func TestRunGenerateWrongHash(t *testing.T) {
	// Save current directory
	origDir, err := os.Getwd()
	require.Nil(t, err)
	defer os.Chdir(origDir)

	// Create temp directory with a generate directive
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	testFile := filepath.Join(dir, "main.go")
	outputFile := filepath.Join(dir, "generated.txt")

	content := "package main\n" +
		"//go:generate sh -c \"echo generated > generated.txt\"\n"
	require.NoError(t, os.WriteFile(testFile, []byte(content), 0644))

	// With wrong hash, command should NOT run and should return error
	err = runGenerate(true, "wronghash123")
	require.NotNil(t, err)
	_, err = os.Stat(outputFile)
	assert.True(t, os.IsNotExist(err))
}

func TestRunGenerateSkip(t *testing.T) {
	// Save current directory
	origDir, err := os.Getwd()
	require.Nil(t, err)
	defer os.Chdir(origDir)

	// Create temp directory with a generate directive
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	testFile := filepath.Join(dir, "main.go")
	outputFile := filepath.Join(dir, "generated.txt")

	content := "package main\n" +
		"//go:generate sh -c \"echo generated > generated.txt\"\n"
	require.NoError(t, os.WriteFile(testFile, []byte(content), 0644))

	// With "skip", command should NOT run but should succeed
	err = runGenerate(true, "skip")
	require.Nil(t, err)
	_, err = os.Stat(outputFile)
	assert.True(t, os.IsNotExist(err))
}

func TestRunGenerateNoDirectives(t *testing.T) {
	// Save current directory
	origDir, err := os.Getwd()
	require.Nil(t, err)
	defer os.Chdir(origDir)

	// Create temp directory with no generate directives
	dir := t.TempDir()
	require.NoError(t, os.Chdir(dir))

	testFile := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package main\n"), 0644))

	err = runGenerate(true, "")
	require.Nil(t, err)
}

func TestComputeDirectivesHash(t *testing.T) {
	directives := []generateDirective{
		{File: "a.go", Line: 1, Command: "echo a"},
		{File: "b.go", Line: 2, Command: "echo b"},
	}

	hash1 := computeDirectivesHash(directives)
	assert.Equal(t, 12, len(hash1))

	// Same directives in different order should produce same hash
	reversed := []generateDirective{
		{File: "b.go", Line: 2, Command: "echo b"},
		{File: "a.go", Line: 1, Command: "echo a"},
	}
	hash2 := computeDirectivesHash(reversed)
	assert.Equal(t, hash2, hash1)

	// Different directives should produce different hash
	different := []generateDirective{
		{File: "a.go", Line: 1, Command: "echo different"},
	}
	hash3 := computeDirectivesHash(different)
	assert.NotEqual(t, hash3, hash1)
}
