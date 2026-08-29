package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

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
	// The build phase resolves the fork toolchain, so a project fixture
	// without one sends every pipeline test to buildhost over the network.
	stubForkToolchain(t)
}

// writeMockBuildOutput writes the file named by a go build's -o flag, as a
// successful compiler does. The t-flavored variant is writeBuildOutput.
func writeMockBuildOutput(cfg runner.Config, content string) {
	for i, arg := range cfg.Args {
		if arg == "-o" && i+1 < len(cfg.Args) {
			os.WriteFile(cfg.Args[i+1], []byte(content), 0o755)
		}
	}
}

// isGoBuild recognizes a `go build`. Every build in this pipeline runs the
// fork's own binary by absolute path, so the command name is
// <forkGoroot>/bin/go rather than the bare "go" that IsCmd matches.
func isGoBuild(cfg runner.Config) bool {
	name := strings.TrimSuffix(filepath.Base(cfg.Name), ".exe")
	return name == "go" && len(cfg.Args) > 0 && cfg.Args[0] == "build"
}

// handleGoBuild leaves the -o target behind, as an exit-0 compiler does;
// every mock reaching the build phase needs it. newBuildFailMock answers first.
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
