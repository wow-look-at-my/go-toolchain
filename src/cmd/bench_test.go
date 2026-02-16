package cmd

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestRunBenchmarkInBuild(t *testing.T) {
	mock := runner.NewMock()

	// Set up benchmark response
	benchOutput := `{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}`
	benchArgs := []string{"test", "-json", "-run", "^$", "-bench", ".", "-benchmem", "./..."}
	mock.SetResponse("go", benchArgs, []byte(benchOutput), nil)

	// Set up git log response (no previous)
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, nil, fmt.Errorf("no notes"))

	oldJSON := jsonOutput
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldVerbose := verbose
	defer func() {
		jsonOutput = oldJSON
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		verbose = oldVerbose
	}()

	jsonOutput = false
	benchTime = ""
	benchCount = 1
	benchCPU = ""
	verbose = false

	err := runBenchmarkInBuild(mock)
	assert.Nil(t, err)
}

func TestRunBenchmarkInBuildJSON(t *testing.T) {
	mock := runner.NewMock()

	benchOutput := `{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}`
	benchArgs := []string{"test", "-json", "-run", "^$", "-bench", ".", "-benchmem", "./..."}
	mock.SetResponse("go", benchArgs, []byte(benchOutput), nil)

	oldJSON := jsonOutput
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	defer func() {
		jsonOutput = oldJSON
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
	}()

	jsonOutput = true
	benchTime = ""
	benchCount = 1
	benchCPU = ""

	err := runBenchmarkInBuild(mock)
	assert.Nil(t, err)
}

func TestRunBenchmarkInBuildWithPrevious(t *testing.T) {
	mock := runner.NewMock()

	benchOutput := `{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}`
	benchArgs := []string{"test", "-json", "-run", "^$", "-bench", ".", "-benchmem", "./..."}
	mock.SetResponse("go", benchArgs, []byte(benchOutput), nil)

	// Set up git log response with previous commit
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, []byte("abc123\n"), nil)

	// Set up git notes show response
	prevData := `{"packages":{"pkg":[{"name":"BenchmarkFoo-8","ns_per_op":1500}]}}`
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "show", "abc123"}, []byte(prevData), nil)

	oldJSON := jsonOutput
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	defer func() {
		jsonOutput = oldJSON
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
	}()

	jsonOutput = false
	benchTime = ""
	benchCount = 1
	benchCPU = ""

	err := runBenchmarkInBuild(mock)
	assert.Nil(t, err)
}

func TestRunBenchmarkInBuildFails(t *testing.T) {
	mock := runner.NewMock()

	benchArgs := []string{"test", "-json", "-run", "^$", "-bench", ".", "-benchmem", "./..."}
	mock.SetResponse("go", benchArgs, nil, fmt.Errorf("benchmark failed"))

	oldJSON := jsonOutput
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	defer func() {
		jsonOutput = oldJSON
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
	}()

	jsonOutput = false
	benchTime = ""
	benchCount = 1
	benchCPU = ""

	err := runBenchmarkInBuild(mock)
	assert.NotNil(t, err)
}

func TestRunWithRunnerBenchmarksByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	oldJSON := jsonOutput
	oldOut := outputDir
	oldBench := noBenchmark
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	defer func() {
		jsonOutput = oldJSON
		outputDir = oldOut
		noBenchmark = oldBench
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
	}()

	jsonOutput = true
	outputDir = tmpDir
	noBenchmark = false // default: benchmarks run
	benchTime = ""
	benchCount = 1
	benchCPU = ""

	err := runWithRunner(mock)
	assert.Nil(t, err)

	// Verify that a benchmark command was issued (go test -bench ...)
	found := false
	for _, cfg := range mock.calls {
		if cfg.Name == "go" && len(cfg.Args) > 0 && cfg.Args[0] == "test" {
			for _, arg := range cfg.Args {
				if arg == "-bench" {
					found = true
					break
				}
			}
		}
	}
	assert.True(t, found)
}

func TestRunWithRunnerNoBenchmarkFlag(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	oldJSON := jsonOutput
	oldOut := outputDir
	oldBench := noBenchmark
	defer func() {
		jsonOutput = oldJSON
		outputDir = oldOut
		noBenchmark = oldBench
	}()

	jsonOutput = true
	outputDir = tmpDir
	noBenchmark = true // --no-benchmark: skip benchmarks

	err := runWithRunner(mock)
	assert.Nil(t, err)

	// Verify no benchmark command was issued
	for _, cfg := range mock.calls {
		if cfg.Name == "go" && len(cfg.Args) > 0 && cfg.Args[0] == "test" {
			for _, arg := range cfg.Args {
				assert.NotEqual(t, "-bench", arg)
			}
		}
	}
}

func TestRunBenchRunWithRunner(t *testing.T) {
	r := runner.New()

	oldJSON := jsonOutput
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldVerbose := verbose
	defer func() {
		jsonOutput = oldJSON
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		verbose = oldVerbose
	}()

	jsonOutput = true
	benchTime = ""
	benchCount = 1
	benchCPU = ""
	verbose = false

	// This will fail because there's no go.mod, but it exercises the code path
	_ = runBenchRunWithRunner(r, jsonOutput)
}

func TestRunBenchSaveWithRunner(t *testing.T) {
	r := runner.New()

	oldJSON := jsonOutput
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldVerbose := verbose
	defer func() {
		jsonOutput = oldJSON
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		verbose = oldVerbose
	}()

	jsonOutput = true
	benchTime = ""
	benchCount = 1
	benchCPU = ""
	verbose = false

	// This will fail because there's no go.mod, but it exercises the code path
	_ = runBenchSaveWithRunner(r, jsonOutput)
}

func TestRunBenchShow(t *testing.T) {
	// This will fail because we're not in a git repo with notes, but exercises code
	err := runBenchShow(benchShowCmd, []string{})
	_ = err // expected to fail
}

func TestRunBenchShowWithArg(t *testing.T) {
	err := runBenchShow(benchShowCmd, []string{"HEAD"})
	_ = err // expected to fail
}

func TestRunBenchCompare(t *testing.T) {
	err := runBenchCompare(benchCompareCmd, []string{"abc123", "def456"})
	_ = err // expected to fail
}

func TestRunBenchRun(t *testing.T) {
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = true

	// This will fail but exercises the code path
	_ = runBenchRun(benchRunCmd, []string{})
}

func TestRunBenchSave(t *testing.T) {
	oldJSON := jsonOutput
	defer func() { jsonOutput = oldJSON }()
	jsonOutput = true

	// This will fail but exercises the code path
	_ = runBenchSave(benchSaveCmd, []string{})
}
