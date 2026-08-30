package bench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// Options configures benchmark execution
type Options struct {
	Time          string // -benchtime
	Count         int    // -count
	CPU           string // -cpu
	Verbose       bool
	StreamTo      io.Writer // if set, benchmark results are printed here as they complete
	OnFirstResult func()    // called before any benchmark result is streamed
}

// RunBenchmarks executes go test -bench and returns parsed results
func RunBenchmarks(r runner.CommandRunner, opts Options) (*BenchmarkReport, error) {
	goTestArgs := buildBenchArgs(opts)
	// Always run with -json so we can parse results
	goTestArgs = append([]string{goTestArgs[0], "-json"}, goTestArgs[1:]...)

	// Clear GOCACHEPROG so the benchmark subprocess doesn't spawn a cacheprog
	// child that inherits stdout and prevents io.ReadAll from completing.
	proc, err := runner.Cmd("go", goTestArgs...).WithHostTarget().WithQuiet().WithEnv("GOCACHEPROG", "").Run(r)
	if err != nil {
		return nil, fmt.Errorf("benchmarks failed: %w", err)
	}
	// Tee stderr while draining it, so a dying process's complaint still prints.
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		io.Copy(os.Stderr, proc.Stderr())
	}()

	// Read stdout line by line so results stream as they complete.
	var buf bytes.Buffer
	var firstOnce sync.Once
	scanner := bufio.NewScanner(proc.Stdout())
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), 1<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		buf.Write(line)
		buf.WriteByte('\n')

		if opts.StreamTo != nil {
			streamBenchResult(line, opts.StreamTo, &firstOnce, opts.OnFirstResult)
		}
	}

	if err := scanner.Err(); err != nil {
		io.Copy(&buf, proc.Stdout())
	}

	waitErr := proc.Wait()
	<-stderrDone
	output := buf.Bytes()

	if waitErr != nil {
		// Say what go test said, or a build failure reports just an exit status.
		err := fmt.Errorf("benchmarks failed: %w", waitErr)
		if diag := Diagnostics(output); diag != "" {
			err = fmt.Errorf("%w\n%s", err, diag)
		}
		// Try to parse and return partial results on failure
		if len(output) > 0 {
			if report, parseErr := ParseBenchmarkOutput(output); parseErr == nil && report.HasResults() {
				return report, err
			}
		}
		return nil, err
	}

	report, err := ParseBenchmarkOutput(output)
	if err != nil {
		return nil, fmt.Errorf("failed to parse benchmark output: %w", err)
	}

	return report, nil
}

func streamBenchResult(line []byte, w io.Writer, once *sync.Once, onFirst func()) {
	var event struct {
		Action string `json:"Action"`
		Output string `json:"Output"`
	}
	if err := json.Unmarshal(line, &event); err != nil {
		return
	}
	if event.Action != "output" {
		return
	}
	trimmed := strings.TrimSpace(event.Output)
	if benchPattern.MatchString(trimmed) {
		if onFirst != nil {
			once.Do(onFirst)
		}
		fmt.Fprintf(w, "    %s\n", trimmed)
	}
}

// HasBenchmarks scans _test.go files under the current directory for
// func Benchmark signatures. Returns true if any are found.
func HasBenchmarks() bool {
	found := false
	filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if name := d.Name(); name == "vendor" || name == "testdata" || (name != "." && strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, "_test.go") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.Contains(data, []byte("\nfunc Benchmark")) {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func buildBenchArgs(opts Options) []string {
	goTestArgs := []string{"test", "-run", "^$", "-bench", ".", "-benchmem"}
	if opts.Time != "" {
		goTestArgs = append(goTestArgs, "-benchtime", opts.Time)
	}
	if opts.Count > 1 {
		goTestArgs = append(goTestArgs, "-count", fmt.Sprintf("%d", opts.Count))
	}
	if opts.CPU != "" {
		goTestArgs = append(goTestArgs, "-cpu", opts.CPU)
	}
	if opts.Verbose {
		goTestArgs = append(goTestArgs, "-v")
	}
	goTestArgs = append(goTestArgs, "./...")
	return goTestArgs
}
