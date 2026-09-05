package test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// parallelArg is the value runTestsOnce passes to both -p and -parallel.
var parallelArg = strconv.Itoa(runtime.NumCPU())

func TestRunTestsWithMock(t *testing.T) {
	t.Serial()
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Create a coverage file for ParseProfile: mostly covered statements
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 17 1
example.com/pkg/main.go:14.20,16.2 3 0
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()
	// Return valid JSON test output
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 85.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-json", "-timeout=" + testTimeout.String(), "-p", parallelArg, "-parallel", parallelArg, "-coverprofile=" + coverFile, "-coverpkg=./...", "-count=1", "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, nil, nil)
	require.Nil(t, err)

	assert.Equal(t, 1, len(result.Coverage.Packages))

	assert.Equal(t, float32(85.0), result.Coverage.Packages[0].Pct())
}

func TestRunTestsFailure(t *testing.T) {
	t.Serial()
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	mock := runner.NewMock()
	mock.SetResponse("go", []string{"test", "-json", "-timeout=" + testTimeout.String(), "-p", parallelArg, "-parallel", parallelArg, "-coverprofile=" + coverFile, "-coverpkg=./...", "-count=1", "./..."}, nil, fmt.Errorf("test failed"))

	_, err := RunTests(mock, false, coverFile, nil, nil)
	assert.NotNil(t, err)
}

func TestRunTestsVerbose(t *testing.T) {
	t.Serial()
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Create coverage file for ParseProfile
	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"=== RUN TestFoo\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 85.0% of statements\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-json", "-timeout=" + testTimeout.String(), "-p", parallelArg, "-parallel", parallelArg, "-coverprofile=" + coverFile, "-coverpkg=./...", "-count=1", "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, true, coverFile, nil, nil) // verbose=true
	require.Nil(t, err)

	assert.Equal(t, 1, len(result.Coverage.Packages))
}

func TestRunTestsNoCoverageFile(t *testing.T) {
	t.Serial()
	coverFile := filepath.Join(t.TempDir(), "coverage.out")
	// Don't create coverage.out - no profile means no statement-level data

	mock := runner.NewMock()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"pkg1","Output":"coverage: 50.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"pkg2","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"pkg2"}
`
	mock.SetResponse("go", []string{"test", "-json", "-timeout=" + testTimeout.String(), "-p", parallelArg, "-parallel", parallelArg, "-coverprofile=" + coverFile, "-coverpkg=./...", "-count=1", "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, nil, nil)
	require.Nil(t, err)

	// No coverage profile means Total stays empty, not a misleading per-package average.
	assert.Equal(t, float32(0), result.Coverage.Total)

	// Per-package percentages are still available from test output
	assert.Equal(t, 2, len(result.Coverage.Packages))
}

func TestRunTestsNoStatementsMarkedCorrectly(t *testing.T) {
	t.Serial()
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Profile only has pkg1 and pkg2 data; pkg3 has no statements. pkg1 is
	// partly covered and pkg2 is fully covered, so the total is weighted.
	coverContent := `mode: set
example.com/pkg1/main.go:10.20,12.2 1 1
example.com/pkg1/main.go:14.20,16.2 1 0
example.com/pkg2/main.go:10.20,12.2 2 1
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg1","Output":"coverage: 50.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"example.com/pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"example.com/pkg2","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"example.com/pkg2"}
{"Time":"2024-01-01T00:00:06Z","Action":"run","Package":"example.com/pkg3"}
{"Time":"2024-01-01T00:00:07Z","Action":"output","Package":"example.com/pkg3","Output":"coverage: [no statements]\n"}
{"Time":"2024-01-01T00:00:08Z","Action":"pass","Package":"example.com/pkg3"}
`
	mock.SetResponse("go", []string{"test", "-json", "-timeout=" + testTimeout.String(), "-p", parallelArg, "-parallel", parallelArg, "-coverprofile=" + coverFile, "-coverpkg=./...", "-count=1", "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, nil, nil)
	require.Nil(t, err)

	// Statement-weighted from the profile, not a per-package average.
	assert.Equal(t, float32(75.0), result.Coverage.Total)

	// Verify statements are set correctly
	for _, p := range result.Coverage.Packages {
		switch p.Package {
		case "example.com/pkg1", "example.com/pkg2":
			assert.NotEqual(t, 0, p.Statements)
		case "example.com/pkg3":
			assert.Equal(t, 0, p.Statements)
		}
	}
}

func TestRunTestsNoStatementsWithProfile(t *testing.T) {
	t.Serial()
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Create coverage file that only has data for pkg1 (pkg2 has no statements)
	coverContent := `mode: set
example.com/pkg1/main.go:10.20,12.2 1 1
example.com/pkg1/main.go:14.20,16.2 1 0
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg1","Output":"coverage: 50.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"example.com/pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"example.com/pkg2","Output":"coverage: [no statements]\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"example.com/pkg2"}
`
	mock.SetResponse("go", []string{"test", "-json", "-timeout=" + testTimeout.String(), "-p", parallelArg, "-parallel", parallelArg, "-coverprofile=" + coverFile, "-coverpkg=./...", "-count=1", "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, nil, nil)
	require.Nil(t, err)

	// Total comes from ParseProfile, weighted by statements
	assert.Equal(t, float32(50.0), result.Coverage.Total)

	// Verify statements are set on the right package
	for _, p := range result.Coverage.Packages {
		assert.False(t, p.Package == "example.com/pkg2" && p.Statements != 0)
		assert.False(t, p.Package == "example.com/pkg1" && p.Statements == 0)
	}
}

func TestRunTestsPackagesContainFiles(t *testing.T) {
	t.Serial()
	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	// Coverage profile with a pair of files in pkg1 and a single file in pkg2
	coverContent := `mode: set
example.com/pkg1/foo.go:10.20,12.2 2 1
example.com/pkg1/bar.go:10.20,12.2 3 1
example.com/pkg2/baz.go:10.20,12.2 5 0
`
	os.WriteFile(coverFile, []byte(coverContent), 0644)

	mock := runner.NewMock()
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg1","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg1"}
{"Time":"2024-01-01T00:00:03Z","Action":"run","Package":"example.com/pkg2"}
{"Time":"2024-01-01T00:00:04Z","Action":"output","Package":"example.com/pkg2","Output":"coverage: 0% of statements\n"}
{"Time":"2024-01-01T00:00:05Z","Action":"pass","Package":"example.com/pkg2"}
`
	mock.SetResponse("go", []string{"test", "-json", "-timeout=" + testTimeout.String(), "-p", parallelArg, "-parallel", parallelArg, "-coverprofile=" + coverFile, "-coverpkg=./...", "-count=1", "./..."}, []byte(testOutput), nil)

	result, err := RunTests(mock, false, coverFile, nil, nil)
	require.Nil(t, err)

	// Verify packages contain their files
	for _, p := range result.Coverage.Packages {
		switch p.Package {
		case "example.com/pkg1":
			assert.Equal(t, 2, len(p.Files))
		case "example.com/pkg2":
			assert.Equal(t, 1, len(p.Files))
		}
	}
}

// setupTestModule creates a temporary directory with a go.mod and test files,
// and returns its root.
func setupTestModule(t *testing.T, modPath string, testPkgDirs []string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+modPath+"\n\ngo 1.25\n"), 0644)
	for _, rel := range testPkgDirs {
		pkgDir := filepath.Join(dir, rel)
		os.MkdirAll(pkgDir, 0755)
		os.WriteFile(filepath.Join(pkgDir, "foo_test.go"), []byte("package "+filepath.Base(rel)+"\n"), 0644)
	}
	return dir
}

func TestListTestPackages(t *testing.T) {
	dir := setupTestModule(t, "example.com/mymod", []string{"pkg1", "pkg2", "pkg3/sub"})
	// Also create a dir with no test files
	os.MkdirAll(filepath.Join(dir, "notest"), 0755)
	os.WriteFile(filepath.Join(dir, "notest", "main.go"), []byte("package notest\n"), 0644)
	// A nested module's packages must not be listed as import paths of the outer module.
	os.MkdirAll(filepath.Join(dir, "nestedmod", "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "nestedmod", "go.mod"), []byte("module example.com/othermodule\n\ngo 1.25\n"), 0644)
	os.WriteFile(filepath.Join(dir, "nestedmod", "sub", "foo_test.go"), []byte("package sub\n"), 0644)

	pkgs := listTestPackages(dir)

	assert.Contains(t, pkgs, "example.com/mymod/pkg1")
	assert.Contains(t, pkgs, "example.com/mymod/pkg2")
	assert.Contains(t, pkgs, "example.com/mymod/pkg3/sub")
	assert.NotContains(t, pkgs, "example.com/mymod/notest")
	assert.NotContains(t, pkgs, "example.com/mymod/nestedmod", "nested module root must be skipped")
	assert.NotContains(t, pkgs, "example.com/mymod/nestedmod/sub", "packages inside a nested module must be skipped")
}

func TestListTestPackagesNoGoMod(t *testing.T) {
	pkgs := listTestPackages(t.TempDir())
	assert.Nil(t, pkgs, "should return nil when no go.mod exists")
}

func TestRunTestsUsesExplicitPackages(t *testing.T) {
	// RunTests reads the working directory.
	t.Chdir(setupTestModule(t, "example.com/proj", []string{"pkg1"}))

	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	coverContent := `mode: set
example.com/proj/pkg1/main.go:10.20,12.2 17 1
example.com/proj/pkg1/main.go:14.20,16.2 3 0
`

	mock := runner.NewMock()

	// With a go.mod present, RunTests uses explicit package list from listTestPackages
	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/proj/pkg1"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/proj/pkg1","Output":"coverage: 85.0% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/proj/pkg1"}
`
	mock.SetResponse("go", []string{"test", "-json", "-timeout=" + testTimeout.String(), "-p", parallelArg, "-parallel", parallelArg, "-coverprofile=" + coverFile, "-coverpkg=./...", "-count=1", "example.com/proj/pkg1"}, []byte(testOutput), nil)

	// Handler writes coverage file when go test runs
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") && cfg.HasArg("-coverprofile="+coverFile) {
			os.WriteFile(coverFile, []byte(coverContent), 0644)
		}
		return nil, nil
	}

	result, err := RunTests(mock, false, coverFile, nil, nil)
	require.Nil(t, err)

	assert.Equal(t, 1, len(result.Coverage.Packages))
	assert.Equal(t, float32(85.0), result.Coverage.Packages[0].Pct())
}

func TestRunTestsFallsBackToEllipsis(t *testing.T) {
	// Run in an empty temp dir with no go.mod — listTestPackages returns nil
	t.Chdir(t.TempDir())

	coverFile := filepath.Join(t.TempDir(), "coverage.out")

	coverContent := `mode: set
example.com/pkg/main.go:10.20,12.2 1 1
`

	mock := runner.NewMock()

	testOutput := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
	mock.SetResponse("go", []string{"test", "-json", "-timeout=" + testTimeout.String(), "-p", parallelArg, "-parallel", parallelArg, "-coverprofile=" + coverFile, "-coverpkg=./...", "-count=1", "./..."}, []byte(testOutput), nil)

	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") && cfg.HasArg("-coverprofile="+coverFile) {
			os.WriteFile(coverFile, []byte(coverContent), 0644)
		}
		return nil, nil
	}

	result, err := RunTests(mock, false, coverFile, nil, nil)
	require.Nil(t, err)
	assert.Equal(t, 1, len(result.Coverage.Packages))
}
