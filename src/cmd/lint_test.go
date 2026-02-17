package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/go-toolchain/src/lint"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

// writeDuplicateGoFiles creates two Go files in dir with near-duplicate functions.
func writeDuplicateGoFiles(t *testing.T, dir string) {
	t.Helper()
	srcA := `package p

func processUser(name string) {
	result := validate(name)
	if result != nil {
		log("validation failed for user")
		return
	}
	data := transform(name)
	if data == nil {
		log("transform failed for user")
		return
	}
	save(data)
	log("saved user")
}
`
	srcB := `package p

func processOrder(id string) {
	result := validate(id)
	if result != nil {
		log("validation failed for order")
		return
	}
	data := transform(id)
	if data == nil {
		log("transform failed for order")
		return
	}
	save(data)
	log("saved order")
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte(srcA), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "b.go"), []byte(srcB), 0644))
}

// writeUniqueGoFile creates a Go file with a single unique function.
func writeUniqueGoFile(t *testing.T, dir, name, funcName string) {
	t.Helper()
	src := `package p

func ` + funcName + `() {
	x := 1
	_ = x
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0644))
}

func TestResolveGoFiles_RecursivePattern(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	require.NoError(t, os.MkdirAll(sub, 0755))
	writeUniqueGoFile(t, dir, "a.go", "a")
	writeUniqueGoFile(t, sub, "b.go", "b")
	// Also a test file that should be excluded
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a_test.go"), []byte("package p"), 0644))

	files, err := resolveGoFiles(dir + "/...")
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

func TestResolveGoFiles_Directory(t *testing.T) {
	dir := t.TempDir()
	writeUniqueGoFile(t, dir, "a.go", "a")
	writeUniqueGoFile(t, dir, "b.go", "b")

	files, err := resolveGoFiles(dir)
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

func TestResolveGoFiles_SingleFile(t *testing.T) {
	dir := t.TempDir()
	writeUniqueGoFile(t, dir, "a.go", "a")

	files, err := resolveGoFiles(filepath.Join(dir, "a.go"))
	require.NoError(t, err)
	assert.Len(t, files, 1)
}

func TestResolveGoFiles_NonexistentGoFile(t *testing.T) {
	files, err := resolveGoFiles("/nonexistent/path.go")
	require.NoError(t, err)
	assert.Equal(t, []string{"/nonexistent/path.go"}, files)
}

func TestResolveGoFiles_NonexistentNonGoFile(t *testing.T) {
	_, err := resolveGoFiles("/nonexistent/path.txt")
	assert.Error(t, err)
}

func TestResolveGoFiles_ExistingNonGoFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "readme.txt")
	require.NoError(t, os.WriteFile(f, []byte("hello"), 0644))

	files, err := resolveGoFiles(f)
	require.NoError(t, err)
	assert.Nil(t, files)
}

func TestWalkGoFiles_SkipsHiddenAndVendor(t *testing.T) {
	dir := t.TempDir()
	hidden := filepath.Join(dir, ".hidden")
	vendor := filepath.Join(dir, "vendor")
	testdata := filepath.Join(dir, "testdata")
	require.NoError(t, os.MkdirAll(hidden, 0755))
	require.NoError(t, os.MkdirAll(vendor, 0755))
	require.NoError(t, os.MkdirAll(testdata, 0755))

	writeUniqueGoFile(t, dir, "good.go", "good")
	writeUniqueGoFile(t, hidden, "hidden.go", "hidden")
	writeUniqueGoFile(t, vendor, "vendor.go", "vendored")
	writeUniqueGoFile(t, testdata, "testdata.go", "testdataFunc")

	files, err := walkGoFiles(dir)
	require.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Contains(t, files[0], "good.go")
}

func TestWalkGoFiles_SkipsTestFiles(t *testing.T) {
	dir := t.TempDir()
	writeUniqueGoFile(t, dir, "main.go", "main")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main_test.go"), []byte("package p"), 0644))

	files, err := walkGoFiles(dir)
	require.NoError(t, err)
	assert.Len(t, files, 1)
	assert.Contains(t, files[0], "main.go")
}

func TestListGoFilesInDir(t *testing.T) {
	dir := t.TempDir()
	writeUniqueGoFile(t, dir, "a.go", "a")
	writeUniqueGoFile(t, dir, "b.go", "b")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "c_test.go"), []byte("package p"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# hi"), 0644))

	files, err := listGoFilesInDir(dir)
	require.NoError(t, err)
	assert.Len(t, files, 2)
}

func TestListGoFilesInDir_NonexistentDir(t *testing.T) {
	_, err := listGoFilesInDir("/nonexistent/dir")
	assert.Error(t, err)
}

func TestRunLintImpl_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeUniqueGoFile(t, dir, "a.go", "funcA")
	writeUniqueGoFile(t, dir, "b.go", "funcB")

	oldJSON := jsonOutput
	jsonOutput = false
	lintThreshold = lint.DefaultThreshold
	lintMinNodes = lint.DefaultMinNodes
	defer func() { jsonOutput = oldJSON }()

	err := runLintImpl([]string{dir})
	assert.NoError(t, err)
}

func TestRunLintImpl_WithDuplicates(t *testing.T) {
	dir := t.TempDir()
	writeDuplicateGoFiles(t, dir)

	oldJSON := jsonOutput
	jsonOutput = false
	lintThreshold = lint.DefaultThreshold
	lintMinNodes = 5
	defer func() {
		jsonOutput = oldJSON
		lintMinNodes = lint.DefaultMinNodes
	}()

	err := runLintImpl([]string{dir})
	assert.NoError(t, err) // warnings only, no error
}

func TestRunLintImpl_JSON(t *testing.T) {
	dir := t.TempDir()
	writeDuplicateGoFiles(t, dir)

	oldJSON := jsonOutput
	jsonOutput = true
	lintThreshold = lint.DefaultThreshold
	lintMinNodes = 5
	defer func() {
		jsonOutput = oldJSON
		lintMinNodes = lint.DefaultMinNodes
	}()

	// Redirect stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err := runLintImpl([]string{dir})

	w.Close()
	os.Stdout = oldStdout

	assert.NoError(t, err)

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	output := string(buf[:n])
	assert.Contains(t, output, "func_a")
}

func TestRunLintImpl_NoGoFiles(t *testing.T) {
	dir := t.TempDir()

	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	err := runLintImpl([]string{dir})
	assert.NoError(t, err)
}

func TestRunLintImpl_DefaultArgs(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	writeUniqueGoFile(t, dir, "a.go", "funcA")

	oldJSON := jsonOutput
	jsonOutput = false
	lintThreshold = lint.DefaultThreshold
	lintMinNodes = lint.DefaultMinNodes
	defer func() { jsonOutput = oldJSON }()

	// Call with no args — should default to ./...
	err := runLintImpl(nil)
	assert.NoError(t, err)
}

func TestRunDuplicateCheck_NoDuplicates(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	writeUniqueGoFile(t, dir, "a.go", "funcA")

	oldJSON := jsonOutput
	jsonOutput = false
	dupcode = true
	lintThreshold = lint.DefaultThreshold
	lintMinNodes = lint.DefaultMinNodes
	defer func() { jsonOutput = oldJSON }()

	// Should not panic or error
	runDuplicateCheck()
}

func TestRunDuplicateCheck_WithDuplicates(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	writeDuplicateGoFiles(t, dir)

	oldJSON := jsonOutput
	oldVerbose := verbose
	jsonOutput = false
	verbose = true
	dupcode = true
	lintThreshold = lint.DefaultThreshold
	lintMinNodes = 5
	defer func() {
		jsonOutput = oldJSON
		verbose = oldVerbose
		lintMinNodes = lint.DefaultMinNodes
	}()

	runDuplicateCheck()
}

func TestRunDuplicateCheck_JSONMode(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	writeDuplicateGoFiles(t, dir)

	oldJSON := jsonOutput
	jsonOutput = true
	dupcode = true
	lintThreshold = lint.DefaultThreshold
	lintMinNodes = 5
	defer func() {
		jsonOutput = oldJSON
		lintMinNodes = lint.DefaultMinNodes
	}()

	// Should silently return in JSON mode
	runDuplicateCheck()
}

func TestRunDuplicateCheck_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(oldWd)

	oldJSON := jsonOutput
	jsonOutput = false
	defer func() { jsonOutput = oldJSON }()

	runDuplicateCheck()
}
