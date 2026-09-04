package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"github.com/wow-look-at-my/go-toolchain/src/vet"
)

// The real bootstrap downloads a toolchain, which spends a test binary's
// whole budget on a cold host cache. The message names the repair.
func init() {
	ensureCosmoToolchainFunc = func() (string, error) {
		return "", fmt.Errorf("test reached the real toolchain bootstrap: call stubForkToolchain(t)")
	}
}

// mockTestEvents renders the `go test -json` stream a passing package
// reports: the run line, the coverage output line, and the pass line. The
// events are marshaled rather than spelled, so the encoder owns the quoting.
func mockTestEvents(pct float32) string {
	const pkg = "example.com/pkg"
	events := []map[string]any{
		{"Time": "2024-01-01T00:00:00Z", "Action": "run", "Package": pkg},
		{"Time": "2024-01-01T00:00:01Z", "Action": "output", "Package": pkg,
			"Output": fmt.Sprintf("coverage: %.1f%% of statements\n", pct)},
		{"Time": "2024-01-01T00:00:02Z", "Action": "pass", "Package": pkg},
	}
	var out strings.Builder
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			panic(err)
		}
		out.Write(raw)
		out.WriteByte('\n')
	}
	return out.String()
}

// writeMockCoverProfileStmts writes a coverage profile with the given
// covered/uncovered statement counts from the -coverprofile= flag in args.
func writeMockCoverProfileStmts(args []string, covered, uncovered int) {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-coverprofile=") {
			continue
		}
		f := strings.TrimPrefix(arg, "-coverprofile=")
		c := "mode: set\n"
		if covered > 0 {
			c += fmt.Sprintf("example.com/pkg/main.go:1.1,2.2 %d 1\n", covered)
		}
		if uncovered > 0 {
			c += fmt.Sprintf("example.com/pkg/main.go:3.1,4.2 %d 0\n", uncovered)
		}
		os.WriteFile(f, []byte(c), 0644)
		return
	}
}

func writeMockCoverProfile(args []string, pct float32) {
	covered := int(pct + 0.5)
	writeMockCoverProfileStmts(args, covered, 100-covered)
}

// handleGoList handles go list commands for mocks. Only go list -deps is still
// shelled out to; module path and main package discovery now use the filesystem.
func handleGoList(cfg runner.Config) (runner.IProcess, bool) {
	if !cfg.IsCmd("go", "list") {
		return nil, false
	}
	for _, arg := range cfg.Args {
		if arg == "-deps" {
			return runner.MockProcess([]byte("example.com/pkg\n"), nil), true
		}
	}
	return nil, false
}

// setupMockProject creates a minimal Go project in the current directory so
// that filesystem-based module path reading and main package discovery work.
func setupMockProject(t *testing.T) {
	t.Helper()
	os.WriteFile("go.mod", []byte("module example.com\n\ngo 1.21\n"), 0644)
	os.MkdirAll("pkg", 0755)
	os.WriteFile("pkg/main.go", []byte("package main\n"), 0644)
	// Without this, the build phase sends every pipeline test to buildhost.
	stubForkToolchain(t)
	stubVetPhase(t)
}

// assertExecutable checks the exec bit where the host keeps such a bit. NT
// does not, so there this asserts only that the file exists.
func assertExecutable(t *testing.T, path, msg string) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	if runtime.GOOS == "windows" {
		return
	}
	assert.NotZero(t, info.Mode().Perm()&0o111, msg)
}

// requireShebangHelper gates a test with a shell-script fixture: NT
// runs no shebang, and a stand-in re-parses the arguments.
func requireShebangHelper(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script; NT runs no shebang")
	}
}

// setHome points os.UserHomeDir() at dir. Windows reads USERPROFILE,
// other hosts HOME.
func setHome(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
}

// stubVetPhase keeps a pipeline test off the real vet pass: vet spawns a go
// list per call, and this package runs the pipeline dozens of times.
func stubVetPhase(t *testing.T) {
	t.Helper()
	t.Serial()
	old := vetRunFunc
	vetRunFunc = func(bool, vet.ProgressFunc) (bool, error) { return false, nil }
	t.Cleanup(func() { vetRunFunc = old })
}

// writeMockBuildOutput writes the -o file, as a real compiler does on success.
func writeMockBuildOutput(cfg runner.Config, content string) {
	for i, arg := range cfg.Args {
		if arg == "-o" && i+1 < len(cfg.Args) {
			os.WriteFile(cfg.Args[i+1], []byte(content), 0o755)
		}
	}
}

// isGoBuild recognizes a `go build`.
func isGoBuild(cfg runner.Config) bool {
	// The fork's binary runs by absolute path, so the name is not the bare "go" IsCmd wants.
	name := strings.TrimSuffix(filepath.Base(cfg.Name), ".exe")
	return name == "go" && len(cfg.Args) > 0 && cfg.Args[0] == "build"
}

// handleGoBuild leaves the -o target behind, as a compiler does on success;
// every mock reaching the build phase needs it. newBuildFailMock takes precedence.
func handleGoBuild(cfg runner.Config) (runner.IProcess, bool) {
	if !isGoBuild(cfg) {
		return nil, false
	}
	writeMockBuildOutput(cfg, "bin")
	return runner.MockProcess(nil, nil), true
}

// newTestPassMock creates a mock runner that passes tests with the given coverage percentage.
// An unset pct defaults to full coverage.
func newTestPassMock(pct float32) *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			covPct := pct
			if covPct == 0 {
				covPct = 100
			}
			writeMockCoverProfile(cfg.Args, covPct)
			return runner.MockProcess([]byte(mockTestEvents(covPct)), nil), nil
		}
		if proc, ok := handleGoBuild(cfg); ok {
			return proc, nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil // fall through to default
	}
	return mock
}

// newTestPipesFailMock creates a mock runner that returns an error when running tests.
func newTestPipesFailMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			return nil, fmt.Errorf("tests failed")
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

// newModTidyFailMock creates a mock runner that fails on go mod tidy.
func newModTidyFailMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "mod") {
			return runner.MockProcess(nil, fmt.Errorf("mod tidy failed")), nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

// newBuildFailMock creates a mock runner that passes tests but fails on go build.
func newBuildFailMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if isGoBuild(cfg) {
			return runner.MockProcess(nil, fmt.Errorf("build failed")), nil
		}
		if cfg.IsCmd("go", "test") {
			writeMockCoverProfile(cfg.Args, 100)
			output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"pass","Package":"example.com/pkg"}
`
			return runner.MockProcess([]byte(output), nil), nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

// newTestFailMock creates a mock runner that returns output with a failed test (but wait() succeeds).
func newTestFailMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			writeMockCoverProfile(cfg.Args, 100)
			output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Output":"coverage: 100% of statements\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"fail","Package":"example.com/pkg"}
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
	return mock
}

// newTestFailWithErrorMock creates a mock runner that returns output AND an error from wait().
func newTestFailWithErrorMock() *runner.Mock {
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			output := `{"Time":"2024-01-01T00:00:00Z","Action":"run","Package":"example.com/pkg","Test":"TestFoo"}
{"Time":"2024-01-01T00:00:01Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"=== RUN   TestFoo\n"}
{"Time":"2024-01-01T00:00:02Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"    foo_test.go:10: assertion failed\n"}
{"Time":"2024-01-01T00:00:03Z","Action":"output","Package":"example.com/pkg","Test":"TestFoo","Output":"--- FAIL: TestFoo (0.00s)\n"}
{"Time":"2024-01-01T00:00:04Z","Action":"fail","Package":"example.com/pkg","Test":"TestFoo"}
{"Time":"2024-01-01T00:00:05Z","Action":"output","Package":"example.com/pkg","Output":"FAIL\n"}
{"Time":"2024-01-01T00:00:06Z","Action":"fail","Package":"example.com/pkg"}
`
			return runner.MockProcess([]byte(output), fmt.Errorf("exit status 1")), nil
		}
		if proc, ok := handleGoList(cfg); ok {
			return proc, nil
		}
		return nil, nil
	}
	return mock
}

func newSmallMock(covered, uncovered int) *runner.Mock {
	pct := float32(covered) / float32(covered+uncovered) * 100
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "test") {
			writeMockCoverProfileStmts(cfg.Args, covered, uncovered)
			return runner.MockProcess([]byte(mockTestEvents(pct)), nil), nil
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
