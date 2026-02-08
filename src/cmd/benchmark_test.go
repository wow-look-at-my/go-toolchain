package cmd

import (
	"fmt"
	"os"
	"testing"
)

func TestBuildBenchArgsDefaults(t *testing.T) {
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldVerbose := verbose
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		verbose = oldVerbose
	}()

	benchTime = ""
	benchCount = 1
	benchCPU = ""
	verbose = false

	args := buildBenchArgs()
	expected := []string{"test", "-run", "^$", "-bench", ".", "-benchmem", "./..."}
	if len(args) != len(expected) {
		t.Fatalf("expected %d args, got %d: %v", len(expected), len(args), args)
	}
	for i, a := range args {
		if a != expected[i] {
			t.Errorf("arg[%d]: expected %q, got %q", i, expected[i], a)
		}
	}
}

func TestBuildBenchArgsAllOptions(t *testing.T) {
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldVerbose := verbose
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		verbose = oldVerbose
	}()

	benchTime = "5s"
	benchCount = 3
	benchCPU = "1,2,4"
	verbose = true

	args := buildBenchArgs()
	assertContains(t, args, "-bench", ".")
	assertContains(t, args, "-benchmem")
	assertContains(t, args, "-benchtime", "5s")
	assertContains(t, args, "-count", "3")
	assertContains(t, args, "-cpu", "1,2,4")
	assertContains(t, args, "-v")
	if args[len(args)-1] != "./..." {
		t.Errorf("expected ./... last, got %q", args[len(args)-1])
	}
}

func TestBuildBenchArgsBenchmemAlwaysPresent(t *testing.T) {
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldVerbose := verbose
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		verbose = oldVerbose
	}()

	benchTime = ""
	benchCount = 1
	benchCPU = ""
	verbose = false

	args := buildBenchArgs()
	found := false
	for _, a := range args {
		if a == "-benchmem" {
			found = true
		}
	}
	if !found {
		t.Error("expected -benchmem to always be present")
	}
}

func TestRunBenchmarkWithRunnerSuccess(t *testing.T) {
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldVerbose := verbose
	oldJSON := jsonOutput
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		verbose = oldVerbose
		jsonOutput = oldJSON
	}()

	benchTime = ""
	benchCount = 1
	benchCPU = ""
	verbose = false
	jsonOutput = false

	mock := NewMockRunner()
	err := runBenchmarkWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(mock.Commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(mock.Commands))
	}
	if mock.Commands[0].Name != "go" {
		t.Errorf("expected 'go' command, got %q", mock.Commands[0].Name)
	}
	if mock.Commands[0].Args[0] != "test" {
		t.Errorf("expected first arg 'test', got %q", mock.Commands[0].Args[0])
	}
}

func TestRunBenchmarkWithRunnerFails(t *testing.T) {
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldJSON := jsonOutput
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		jsonOutput = oldJSON
	}()

	benchTime = ""
	benchCount = 1
	benchCPU = ""
	jsonOutput = false

	mock := NewMockRunner()
	mock.SetResponse("go", buildBenchArgs(), nil, fmt.Errorf("benchmark failed"))
	err := runBenchmarkWithRunner(mock)
	if err == nil {
		t.Error("expected error when benchmarks fail")
	}
}

func TestRunBenchmarkWithRunnerJSON(t *testing.T) {
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldJSON := jsonOutput
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		jsonOutput = oldJSON
	}()

	benchTime = ""
	benchCount = 1
	benchCPU = ""
	jsonOutput = true

	mock := NewMockRunner()
	jsonArgs := append([]string{"test", "-json"}, buildBenchArgs()[1:]...)
	mock.SetResponse("go", jsonArgs, []byte(`{"Output":"BenchmarkFoo 1000 1234 ns/op\n"}`), nil)

	err := runBenchmarkWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunWithRunnerBenchmarkFlag(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	oldJSON := jsonOutput
	oldMin := minCoverage
	oldOut := outputDir
	oldBench := doBenchmark
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	defer func() {
		jsonOutput = oldJSON
		minCoverage = oldMin
		outputDir = oldOut
		doBenchmark = oldBench
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
	}()

	jsonOutput = true
	minCoverage = 80
	outputDir = tmpDir
	doBenchmark = true
	benchTime = ""
	benchCount = 1
	benchCPU = ""

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify that a benchmark command was issued (go test -bench ...)
	found := false
	for _, cmd := range mock.commands {
		if cmd.Name == "go" && len(cmd.Args) > 0 && cmd.Args[0] == "test" {
			for _, arg := range cmd.Args {
				if arg == "-bench" {
					found = true
					break
				}
			}
		}
	}
	if !found {
		t.Error("expected benchmark command (go test -bench ...) to be run when --benchmark is set")
	}
}

func TestRunWithRunnerNoBenchmarkByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := &testPassMockRunner{}

	oldJSON := jsonOutput
	oldMin := minCoverage
	oldOut := outputDir
	oldBench := doBenchmark
	defer func() {
		jsonOutput = oldJSON
		minCoverage = oldMin
		outputDir = oldOut
		doBenchmark = oldBench
	}()

	jsonOutput = true
	minCoverage = 80
	outputDir = tmpDir
	doBenchmark = false

	err := runWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify no benchmark command was issued
	for _, cmd := range mock.commands {
		if cmd.Name == "go" && len(cmd.Args) > 0 && cmd.Args[0] == "test" {
			for _, arg := range cmd.Args {
				if arg == "-bench" {
					t.Error("benchmark command should not be run when --benchmark is not set")
				}
			}
		}
	}
}

// assertContains checks that args contains the given sequence of values
func assertContains(t *testing.T, args []string, values ...string) {
	t.Helper()
	for i, a := range args {
		if a == values[0] {
			if len(values) == 1 {
				return
			}
			if i+1 < len(args) && args[i+1] == values[1] {
				return
			}
		}
	}
	t.Errorf("args %v does not contain %v", args, values)
}
