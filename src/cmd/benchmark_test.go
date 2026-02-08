package cmd

import (
	"fmt"
	"testing"
)

func TestBuildBenchArgsDefaults(t *testing.T) {
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldMem := benchMem
	oldVerbose := verbose
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		benchMem = oldMem
		verbose = oldVerbose
	}()

	benchTime = ""
	benchCount = 1
	benchCPU = ""
	benchMem = true
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
	oldMem := benchMem
	oldVerbose := verbose
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		benchMem = oldMem
		verbose = oldVerbose
	}()

	benchTime = "5s"
	benchCount = 3
	benchCPU = "1,2,4"
	benchMem = true
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

func TestBuildBenchArgsNoMem(t *testing.T) {
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldMem := benchMem
	oldVerbose := verbose
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		benchMem = oldMem
		verbose = oldVerbose
	}()

	benchTime = ""
	benchCount = 1
	benchCPU = ""
	benchMem = false
	verbose = false

	args := buildBenchArgs()
	for _, a := range args {
		if a == "-benchmem" {
			t.Error("expected -benchmem to be absent when benchMem is false")
		}
	}
}

func TestRunBenchmarkWithRunnerSuccess(t *testing.T) {
	oldTime := benchTime
	oldCount := benchCount
	oldCPU := benchCPU
	oldMem := benchMem
	oldVerbose := verbose
	oldJSON := jsonOutput
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		benchMem = oldMem
		verbose = oldVerbose
		jsonOutput = oldJSON
	}()

	benchTime = ""
	benchCount = 1
	benchCPU = ""
	benchMem = true
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
	oldMem := benchMem
	oldJSON := jsonOutput
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		benchMem = oldMem
		jsonOutput = oldJSON
	}()

	benchTime = ""
	benchCount = 1
	benchCPU = ""
	benchMem = true
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
	oldMem := benchMem
	oldJSON := jsonOutput
	defer func() {
		benchTime = oldTime
		benchCount = oldCount
		benchCPU = oldCPU
		benchMem = oldMem
		jsonOutput = oldJSON
	}()

	benchTime = ""
	benchCount = 1
	benchCPU = ""
	benchMem = true
	jsonOutput = true

	mock := NewMockRunner()
	jsonArgs := append([]string{"test", "-json"}, buildBenchArgs()[1:]...)
	mock.SetResponse("go", jsonArgs, []byte(`{"Output":"BenchmarkFoo 1000 1234 ns/op\n"}`), nil)

	err := runBenchmarkWithRunner(mock)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
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
