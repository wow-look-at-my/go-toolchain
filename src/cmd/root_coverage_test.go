package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/go-toolchain/src/summary"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
)

// TestRunWithRunnerActiveTrace exercises the per-test trace recording path in
// RunTestsWithCoverage. It sets activeTrace, provides a mock with test-level
// events covering all branches of the recording loop (zero-elapsed skip,
// parent-has-subtest skip, and a normal recorded leaf test), and passes a
// non-nil SummaryData to cover the summary accumulation code path.
func TestRunWithRunnerActiveTrace(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()

	oldTrace := activeTrace
	activeTrace = gotrace.NewTrace()
	defer func() { activeTrace = oldTrace }()

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			writeMockCoverProfile(cfg.Args, 100)
			// TestNoElapsed: no Elapsed → 0, hits tc.Elapsed<=0 early-continue path.
			// TestParent/Sub: subtest pair, TestParent ends up in hasSubtest and is skipped.
			// TestLeaf: plain test with Elapsed>0, gets recorded in the trace.
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
// second mod-tidy step.
func TestRunWithRunnerGenerateSkip(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)
	setupMockProject()

	// Add a //go:generate directive so needsGenerate() returns true.
	os.WriteFile(filepath.Join(tmpDir, "pkg", "main.go"), []byte("package main\n//go:generate echo hello\n"), 0644)

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
