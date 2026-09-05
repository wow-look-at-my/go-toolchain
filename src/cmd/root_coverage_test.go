package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
)

// TestRunWithRunnerActiveTrace exercises the per-test trace recording path in
// RunTestsWithCoverage. It sets activeTrace, provides a mock with test-level
// events covering all branches of the recording loop (an elapsed-free skip,
// parent-has-subtest skip, and a normal recorded leaf test), and passes a
// non-nil SummaryData to cover the summary accumulation code path.
func TestRunWithRunnerActiveTrace(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	setupMockProject(t)

	oldTrace := activeTrace
	activeTrace = gotrace.NewTrace()
	defer func() { activeTrace = oldTrace }()

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			writeMockCoverProfile(cfg.Args, 100)
			// TestNoElapsed: no Elapsed, hits the early-continue path for an unmeasured test.
			// TestParent/Sub: subtest pair, TestParent ends up in hasSubtest and is skipped.
			// TestLeaf: plain test with a measured Elapsed, gets recorded in the trace.
			output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg","Test":"TestNoElapsed"}
{"Time":"2024-01-01T00:00:00Z","Action":"pass","Package":"example.com/pkg","Test":"TestNoElapsed"}
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg","Test":"TestParent"}
{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg","Test":"TestParent/Sub"}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"example.com/pkg","Test":"TestParent/Sub","Elapsed":0.01}
{"Time":"2024-01-01T00:00:01Z","Action":"pass","Package":"example.com/pkg","Test":"TestParent","Elapsed":0.02}
{"Time":"2024-01-01T00:00:01Z","Action":"run","Package":"example.com/pkg","Test":"TestLeaf"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg","Test":"TestLeaf","Elapsed":0.03}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
			return runner.MockProcess([]byte(output), nil), nil
		}
		if proc, ok := handleGoBuild(cfg); ok {
			return proc, nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}

	sd := &summary.SummaryData{}
	jsonOutput = false
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock, sd)
	assert.Nil(t, err)
	assert.NotNil(t, sd.Coverage)
}

// TestRunWithRunnerGenerateSkip exercises the needsGenerate() → true branch and
// the generateHash="skip" path through runGenerate, including the post-generate
// repeat mod-tidy step.
func TestRunWithRunnerGenerateSkip(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	setupMockProject(t)

	// Add a //go:generate directive so needsGenerate() returns true.
	os.WriteFile(filepath.Join(tmpDir, "pkg", "main.go"), []byte("package main\n\n//go:generate echo hello\n"), 0644)

	oldHash := generateHash
	generateHash = "skip"
	defer func() { generateHash = oldHash }()

	mock := newTestPassMock(0)
	jsonOutput = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()

	err := runWithRunner(mock, nil)
	assert.Nil(t, err)
}

// newNoTestFilesMock simulates `go test ./...` on a module with no test
// files: the package appears in the JSON stream only as a skip
// ("?   pkg [no test files]"), the run exits clean, and the coverage profile
// stays empty (just "mode: set") because no test binary ever ran.
func newNoTestFilesMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			writeMockCoverProfileStmts(cfg.Args, 0, 0)
			out := `{"Time":"2024-01-01T00:00:00Z","Action":"output","Package":"example.com/pkg","Output":"?   \texample.com/pkg\t[no test files]\n"}` + "\n" +
				`{"Time":"2024-01-01T00:00:01Z","Action":"skip","Package":"example.com/pkg"}` + "\n"
			return runner.MockProcess([]byte(out), nil), nil
		}
		if proc, ok := handleGoBuild(cfg); ok {
			return proc, nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

// TestRunWithRunnerZeroStatementModulePasses: a module whose packages have no
// test files and no coverable statements (e.g. an embed-only module like
// uasset-decoder's web/) must pass the coverage check vacuously instead of
// panicking with "coverage data is missing or broken".
func TestRunWithRunnerZeroStatementModulePasses(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	setupMockProject(t) // pkg/main.go is "package main\n" — no coverable statements

	mock := newNoTestFilesMock()
	jsonOutput = true
	outputDir = tmpDir
	defer func() {
		jsonOutput = false
		outputDir = "build"
	}()
	assert.NotPanics(t, func() {
		assert.Nil(t, runWithRunner(mock, nil))
	})
}

// TestRunWithRunnerNoTestsWithCodeFails: a module WITH coverable statements
// but no tests at all must fail with an actionable error, not a panic.
func TestRunWithRunnerNoTestsWithCodeFails(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	setupMockProject(t)
	os.WriteFile(filepath.Join("pkg", "main.go"), []byte("package main\n\nfunc main() { println(\"x\") }\n"), 0644)

	mock := newNoTestFilesMock()
	jsonOutput = true
	defer func() { jsonOutput = false }()
	assert.NotPanics(t, func() {
		err := runWithRunner(mock, nil)
		assert.NotNil(t, err)
		if err != nil {
			assert.Contains(t, err.Error(), "no test results")
		}
	})
}
