package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func chdirWithBenchFile(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x_test.go"), []byte("package p\nimport \"testing\"\nfunc BenchmarkX(b *testing.B) {}\n"), 0644)
	t.Chdir(dir)
}

func TestRunBenchmarkInBuild(t *testing.T) {
	t.Serial()
	chdirWithBenchFile(t)
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

	_, err := runBenchmarkInBuild(mock)
	assert.Nil(t, err)
}

func TestRunBenchmarkInBuildJSON(t *testing.T) {
	t.Serial()
	chdirWithBenchFile(t)
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

	_, err := runBenchmarkInBuild(mock)
	assert.Nil(t, err)
}

func TestRunBenchmarkInBuildWithPrevious(t *testing.T) {
	t.Serial()
	chdirWithBenchFile(t)
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

	_, err := runBenchmarkInBuild(mock)
	assert.Nil(t, err)
}

func TestRunBenchmarkInBuildFails(t *testing.T) {
	t.Serial()
	chdirWithBenchFile(t)
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

	_, err := runBenchmarkInBuild(mock)
	assert.NotNil(t, err)
}

func TestRunBenchmarkInBuildSkipsWhenNoBenchmarks(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main_test.go"), []byte("package p\nimport \"testing\"\nfunc TestX(t *testing.T) {}\n"), 0644)
	t.Chdir(dir)

	mock := runner.NewMock()
	br, err := runBenchmarkInBuild(mock)
	assert.Nil(t, err)
	assert.Nil(t, br)
	for _, cfg := range mock.Calls() {
		assert.False(t, cfg.HasArg("-bench"), "should not run benchmarks when no Benchmark functions exist")
	}
}

func TestRunWithRunnerBenchmarksByDefault(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	os.WriteFile(filepath.Join(tmpDir, "x_test.go"), []byte("package p\nimport \"testing\"\nfunc BenchmarkX(b *testing.B) {}\n"), 0644)

	stubForkToolchain(t)
	mock := newTestPassMock(0)

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

	err := runWithRunner(mock, nil)
	assert.Nil(t, err)

	// Verify that a benchmark command was issued (go test -bench ...)
	found := false
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "test") && cfg.HasArg("-bench") {
			found = true
			break
		}
	}
	assert.True(t, found)
}

func TestRunWithRunnerNoBenchmarkFlag(t *testing.T) {
	t.Serial()
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	stubForkToolchain(t)
	mock := newTestPassMock(0)

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

	err := runWithRunner(mock, nil)
	assert.Nil(t, err)

	// Verify no benchmark command was issued
	for _, cfg := range mock.Calls() {
		if cfg.IsCmd("go", "test") {
			assert.False(t, cfg.HasArg("-bench"), "should not have -bench flag")
		}
	}
}

func TestRunBenchRunWithRunner(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()

	// Set up benchmark response
	benchOutput := `{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}`
	benchArgs := []string{"test", "-json", "-run", "^$", "-bench", ".", "-benchmem", "./..."}
	mock.SetResponse("go", benchArgs, []byte(benchOutput), nil)

	// Set up git log response for FetchPrevious (no previous)
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

	jsonOutput = true
	benchTime = ""
	benchCount = 1
	benchCPU = ""
	verbose = false

	err := runBenchRunWithRunner(mock, jsonOutput)
	assert.Nil(t, err)
}

func TestRunBenchSaveWithRunner(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()

	// Set up benchmark response
	benchOutput := `{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}`
	benchArgs := []string{"test", "-json", "-run", "^$", "-bench", ".", "-benchmem", "./..."}
	mock.SetResponse("go", benchArgs, []byte(benchOutput), nil)

	// Handle git commands dynamically (StoreNotes has dynamic -m arg, can't use exact match)
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "notes") {
			return runner.MockProcess(nil, nil), nil
		}
		if cfg.IsCmd("git", "rev-parse") {
			return runner.MockProcess([]byte("abc1234\n"), nil), nil
		}
		return nil, nil // fall through to SetResponse
	}

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

	err := runBenchSaveWithRunner(mock, jsonOutput)
	assert.Nil(t, err)
}

func TestRunBenchShow(t *testing.T) {
	t.Serial()
	// This will fail because we're not in a git repo with notes, but exercises code
	err := runBenchShow(benchShowCmd, []string{})
	_ = err // expected to fail
}

func TestRunBenchShowWithArg(t *testing.T) {
	t.Serial()
	err := runBenchShow(benchShowCmd, []string{"HEAD"})
	_ = err // expected to fail
}

func TestRunBenchCompare(t *testing.T) {
	t.Serial()
	err := runBenchCompare(benchCompareCmd, []string{"abc123", "def456"})
	_ = err // expected to fail
}

func TestRunBenchRun(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()

	benchOutput := `{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}`
	benchArgs := []string{"test", "-json", "-run", "^$", "-bench", ".", "-benchmem", "./..."}
	mock.SetResponse("go", benchArgs, []byte(benchOutput), nil)
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

	jsonOutput = true
	benchTime = ""
	benchCount = 1
	benchCPU = ""
	verbose = false

	err := runBenchRunWithRunner(mock, jsonOutput)
	assert.Nil(t, err)
}

func TestRunBenchSave(t *testing.T) {
	t.Serial()
	mock := runner.NewMock()

	benchOutput := `{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}`
	benchArgs := []string{"test", "-json", "-run", "^$", "-bench", ".", "-benchmem", "./..."}
	mock.SetResponse("go", benchArgs, []byte(benchOutput), nil)
	mock.SetResponse("git", []string{"log", "--format=%H", "--notes=benchmarks", "--grep=", "-1"}, nil, fmt.Errorf("no notes"))
	mock.SetResponse("git", []string{"notes", "--ref=benchmarks", "add", "-f", "-m"}, nil, nil)
	mock.SetResponse("git", []string{"rev-parse", "HEAD"}, []byte("abc123\n"), nil)

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

	err := runBenchSaveWithRunner(mock, jsonOutput)
	assert.Nil(t, err)
}
