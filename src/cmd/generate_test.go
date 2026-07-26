package cmd

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestParseDirectivesLongLines(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")

	// Build a file with a line far exceeding bufio's default 4KB buffer.
	// The directive must still be found on lines before and after the long line.
	longLine := "var x = \"" + strings.Repeat("A", 100_000) + "\"\n"

	content := "package main\n\n" +
		"//go:generate echo before\n" +
		longLine +
		"//go:generate echo after\n"
	require.NoError(t, os.WriteFile(testFile, []byte(content), 0644))

	directives, err := parseDirectives(testFile)
	require.Nil(t, err)

	require.Equal(t, 2, len(directives))
	assert.Equal(t, "echo before", directives[0].Command)
	assert.Equal(t, 3, directives[0].Line)
	assert.Equal(t, "echo after", directives[1].Command)
	assert.Equal(t, 5, directives[1].Line)
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

func TestParseDirectivesRejectsGoFmt(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")

	content := "package main\n\n" +
		"//go:generate go fmt ./...\n"
	require.NoError(t, os.WriteFile(testFile, []byte(content), 0644))

	_, err := parseDirectives(testFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "go fmt is not allowed")
}

func TestParseDirectivesRejectsShellWrappedGoFmt(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")

	content := "package main\n\n" +
		"//go:generate sh -c \"go fmt ./...\"\n"
	require.NoError(t, os.WriteFile(testFile, []byte(content), 0644))

	_, err := parseDirectives(testFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "go fmt is not allowed")
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
		Command: "false",
	}

	err := executeDirective(d, true)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "generate failed")
}

// A directive's output must reach the console while the command is still
// running. It used to be buffered until exit, which hid it from the output
// watchdog (it monitors fd 1/2) and made every slow directive -- `go run
// <tool>@latest` downloading modules, say -- print one STALLED banner per
// second for its whole duration.
//
// The helper announces itself and then waits, bounded, for the test to react to
// that announcement. Buffered output means the reaction never arrives in time
// and the helper exits non-zero, so this fails on regression rather than
// racing a wall-clock deadline.
func TestExecuteDirectiveStreamsOutputWhileRunning(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package main\n"), 0644))

	sentinel := filepath.Join(dir, "reader-saw-it")
	helper := filepath.Join(dir, "announce-then-wait")
	script := "#!/bin/sh\n" +
		"echo streaming-marker\n" +
		"i=0\n" +
		"while [ \"$i\" -lt 100 ]; do\n" +
		"  [ -f " + sentinel + " ] && exit 0\n" +
		"  sleep 0.1\n" +
		"  i=$((i+1))\n" +
		"done\n" +
		"exit 1\n"
	require.NoError(t, os.WriteFile(helper, []byte(script), 0755))

	r, w, err := os.Pipe()
	require.NoError(t, err)
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	go func() {
		br := bufio.NewReader(r)
		for {
			line, err := br.ReadString('\n')
			if strings.Contains(line, "streaming-marker") {
				_ = os.WriteFile(sentinel, nil, 0644)
			}
			if err != nil {
				return
			}
		}
	}()

	d := generateDirective{File: testFile, Line: 1, Command: helper}
	runErr := executeDirective(d, false)

	os.Stdout = old
	w.Close()
	require.NoError(t, runErr, "output was not visible until the command exited")
}

func TestStreamPrefixWriter(t *testing.T) {
	var w streamPrefixWriter

	// A complete line is emitted on arrival; a partial one waits for Flush.
	out := captureStdout(func() {
		n, err := w.Write([]byte("first\nsecond"))
		require.NoError(t, err)
		assert.Equal(t, len("first\nsecond"), n)
	})
	assert.Equal(t, "\t> first\n", out)

	out = captureStdout(w.Flush)
	assert.Equal(t, "\t> second\n", out)

	// Flush with nothing pending emits nothing, and CRLF loses the CR.
	out = captureStdout(func() {
		w.Flush()
		_, _ = w.Write([]byte("crlf\r\n"))
	})
	assert.Equal(t, "\t> crlf\n", out)

	// The same shape prefixOutput produces for the buffered (quiet) path.
	out = captureStdout(func() { _, _ = w.Write([]byte("a\nb\n")) })
	assert.Equal(t, prefixOutput("a\nb\n"), out)
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

func TestSplitGenerateCommand(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{
			name:  "simple",
			input: "echo hello",
			want:  []string{"echo", "hello"},
		},
		{
			name:  "double quoted",
			input: `sh -c "echo hello world"`,
			want:  []string{"sh", "-c", "echo hello world"},
		},
		{
			name:  "backtick quoted",
			input: "sh -c `echo hello world`",
			want:  []string{"sh", "-c", "echo hello world"},
		},
		{
			name:  "brace expansion with comma",
			input: `go run ./cmd/gen -regex [a-z]+@[a-z]+\.[a-z]{2,}`,
			want:  []string{"go", "run", "./cmd/gen", "-regex", `[a-z]+@[a-z]+\.[a-z]{2,}`},
		},
		{
			name:  "glob star",
			input: `go run ./cmd/gen -regex [A-Za-z_][A-Za-z0-9_]*`,
			want:  []string{"go", "run", "./cmd/gen", "-regex", `[A-Za-z_][A-Za-z0-9_]*`},
		},
		{
			name:  "parentheses and question mark",
			input: `go run ./cmd/gen -regex (https?://)?[a-z]+\.[a-z]{2,}`,
			want:  []string{"go", "run", "./cmd/gen", "-regex", `(https?://)?[a-z]+\.[a-z]{2,}`},
		},
		{
			name:  "hash character",
			input: `go run ./cmd/gen -regex #[0-9a-f]{6}`,
			want:  []string{"go", "run", "./cmd/gen", "-regex", `#[0-9a-f]{6}`},
		},
		{
			name:  "backslash digit sequence",
			input: `go run ./cmd/gen -regex \d{3}-\d{2}-\d{4}`,
			want:  []string{"go", "run", "./cmd/gen", "-regex", `\d{3}-\d{2}-\d{4}`},
		},
		{
			name:  "group with flag",
			input: `go run ./cmd/gen -regex (?i)hello`,
			want:  []string{"go", "run", "./cmd/gen", "-regex", `(?i)hello`},
		},
		{
			name:  "extra whitespace",
			input: "  echo   hello  world  ",
			want:  []string{"echo", "hello", "world"},
		},
		{
			name:  "tabs",
			input: "echo\thello",
			want:  []string{"echo", "hello"},
		},
		{
			name:  "escaped chars in double quotes",
			input: `echo "hello\tworld"`,
			want:  []string{"echo", "hello\tworld"},
		},
		{
			name:    "unterminated double quote",
			input:   `echo "hello`,
			wantErr: true,
		},
		{
			name:    "unterminated backtick",
			input:   "echo `hello",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := splitGenerateCommand(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExpandGenerateVars(t *testing.T) {
	d := generateDirective{
		File:    "sub/foo.go",
		Line:    42,
		Command: "ignored",
	}

	got := expandGenerateVars("$GOFILE $GOLINE $GOPACKAGE $GOARCH $GOOS", d)
	assert.Contains(t, got, "foo.go")
	assert.Contains(t, got, "42")
	assert.Contains(t, got, "sub")
	assert.Contains(t, got, runtime.GOARCH)
	assert.Contains(t, got, runtime.GOOS)

	got = expandGenerateVars("price is $DOLLAR 5", d)
	assert.Equal(t, "price is $ 5", got)
}

func TestExecuteDirectivePreservesMetachars(t *testing.T) {
	dir := t.TempDir()

	helper := filepath.Join(dir, "dump-args")
	outFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\nfor arg; do printf '%s\\n' \"$arg\"; done > " + outFile + "\n"
	require.NoError(t, os.WriteFile(helper, []byte(script), 0755))

	testFile := filepath.Join(dir, "test.go")
	require.NoError(t, os.WriteFile(testFile, []byte("package main\n"), 0644))

	d := generateDirective{
		File:    testFile,
		Line:    1,
		Command: helper + ` -regex [A-Za-z_][A-Za-z0-9_]* -func E`,
	}

	err := executeDirective(d, true)
	require.Nil(t, err)

	content, err := os.ReadFile(outFile)
	require.Nil(t, err)
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	assert.Equal(t, []string{"-regex", "[A-Za-z_][A-Za-z0-9_]*", "-func", "E"}, lines)
}
