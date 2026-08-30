package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// corruptIndexMarker is cmd/go's error for an unparseable cached module index; it has no build id to gate on.
const corruptIndexMarker = "corrupt index"

// tailBufferCap bounds how much subprocess stderr tailBuffer retains.
const tailBufferCap = 64 << 10

// tailBuffer keeps a stream's last tailBufferCap bytes, bounding memory use.
type tailBuffer struct {
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailBufferCap {
		t.buf = t.buf[len(t.buf)-tailBufferCap:]
	}
	return len(p), nil
}

func (t *tailBuffer) String() string { return string(t.buf) }

// disableGoModuleIndex disables cmd/go's module index for this process and its children; it only slows scans.
func disableGoModuleIndex() {
	godebug := os.Getenv("GODEBUG")
	if godebug != "" {
		godebug += ","
	}
	os.Setenv("GODEBUG", godebug+"goindex=0")
}

// runModTidy runs `go mod tidy -v` through r. When cmd/go fails reporting a
// corrupt Go module index, the module index is disabled for the remainder of
// this run (every later phase would read the same damaged entry) and the tidy
// is retried a single time.
func runModTidy(r runner.CommandRunner, quiet bool) error {
	var modTidyStep *step
	if !quiet {
		modTidyStep = logStep("go mod tidy")
	}

	tidyOnce := func() (string, error) {
		timedStderr := newTimedLineWriter(os.Stderr)
		var tail tailBuffer
		proc, err := runner.Cmd("go", "mod", "tidy", "-v").WithStderrWriter(io.MultiWriter(timedStderr, &tail)).WithOnFirstOutput(func() {
			if modTidyStep != nil {
				modTidyStep.noteOutput()
			}
		}).Run(r)
		if err != nil {
			return tail.String(), err
		}
		err = proc.Wait()
		timedStderr.Flush()
		return tail.String(), err
	}

	stderrTail, err := tidyOnce()
	if err != nil && strings.Contains(stderrTail, corruptIndexMarker) {
		logger.Warn("go mod tidy reported a corrupt Go module index (a damaged build-cache entry); disabling the module index (GODEBUG=goindex=0) for the rest of this run and retrying")
		disableGoModuleIndex()
		_, err = tidyOnce()
	}
	if err != nil {
		if _, statErr := os.Stat("go.mod"); statErr != nil {
			return fmt.Errorf("no go.mod found — initialize with: go mod init <module-path>")
		}
		return fmt.Errorf("go mod tidy failed: %w", err)
	}
	if modTidyStep != nil {
		modTidyStep.done()
	}
	return nil
}
