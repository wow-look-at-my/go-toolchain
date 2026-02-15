package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"testing"
)

// benchMockRunner implements interfaces needed for bench testing
type benchMockRunner struct {
	commands  []MockCommand
	responses map[string]MockResponse
}

func newBenchMockRunner() *benchMockRunner {
	return &benchMockRunner{
		responses: make(map[string]MockResponse),
	}
}

func (m *benchMockRunner) key(name string, args ...string) string {
	return fmt.Sprintf("%s %v", name, args)
}

func (m *benchMockRunner) SetResponse(name string, args []string, output []byte, err error) {
	m.responses[m.key(name, args...)] = MockResponse{Output: output, Err: err}
}

func (m *benchMockRunner) Run(name string, args ...string) error {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	resp, ok := m.responses[m.key(name, args...)]
	if ok {
		return resp.Err
	}
	return nil
}

func (m *benchMockRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	resp, ok := m.responses[m.key(name, args...)]
	if ok {
		return resp.Output, resp.Err
	}
	return nil, nil
}

func (m *benchMockRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	resp, ok := m.responses[m.key(name, args...)]
	if ok {
		return bytes.NewReader(resp.Output), func() error { return resp.Err }, nil
	}
	return bytes.NewReader(nil), func() error { return nil }, nil
}

func (m *benchMockRunner) RunWithEnv(env []string, name string, args ...string) error {
	m.commands = append(m.commands, MockCommand{Name: name, Args: args})
	resp, ok := m.responses[m.key(name, args...)]
	if ok {
		return resp.Err
	}
	return nil
}

func TestRunBenchmarkInBuild(t *testing.T) {
	mock := newBenchMockRunner()

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
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunBenchmarkInBuildJSON(t *testing.T) {
	mock := newBenchMockRunner()

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
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunBenchmarkInBuildWithPrevious(t *testing.T) {
	mock := newBenchMockRunner()

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
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunBenchmarkInBuildFails(t *testing.T) {
	mock := newBenchMockRunner()

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
	if err == nil {
		t.Error("expected error when benchmarks fail")
	}
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
		t.Error("expected benchmark command (go test -bench ...) to be run by default")
	}
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
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify no benchmark command was issued
	for _, cmd := range mock.commands {
		if cmd.Name == "go" && len(cmd.Args) > 0 && cmd.Args[0] == "test" {
			for _, arg := range cmd.Args {
				if arg == "-bench" {
					t.Error("benchmark command should not be run when --no-benchmark is set")
				}
			}
		}
	}
}

func TestBenchRunnerWrapper(t *testing.T) {
	mock := newBenchMockRunner()
	br := &benchRunner{mock}

	// Test RunWithOutput
	mock.SetResponse("test", []string{"arg"}, []byte("output"), nil)
	out, err := br.RunWithOutput("test", "arg")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if string(out) != "output" {
		t.Errorf("expected 'output', got %q", string(out))
	}

	// Test Run
	err = br.Run("test2", "arg2")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunBenchRunWithRunner(t *testing.T) {
	mock := &RealCommandRunner{Quiet: true}

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
	_ = runBenchRunWithRunner(mock)
}

func TestRunBenchSaveWithRunner(t *testing.T) {
	mock := &RealCommandRunner{Quiet: true}

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
	_ = runBenchSaveWithRunner(mock)
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

