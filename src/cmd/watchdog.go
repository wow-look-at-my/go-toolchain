package cmd

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// activeWatchdog is the current output watchdog, if any.
// Accessed by the step system to report current step names.
var activeWatchdog *outputWatchdog

// outputWatchdog monitors all stdout/stderr output and warns when the build
// goes silent for too long. It intercepts file descriptors 1 and 2 via dup2
// so that nothing can bypass it.
type outputWatchdog struct {
	origStdout *os.File // saved original stdout
	origStderr *os.File // saved original stderr
	stdoutR    *os.File // pipe read-end for stdout
	stderrR    *os.File // pipe read-end for stderr
	lastOutput atomic.Int64
	stepName   atomic.Value // string
	threshold  time.Duration
	cancel     context.CancelFunc
	done       chan struct{}
	fwdWG      sync.WaitGroup // tracks forward() goroutines so stop() can wait for full drain
}

// forward reads from src (pipe read-end) and writes to dst (original fd),
// updating lastOutput on every successful read.
func (w *outputWatchdog) forward(src, dst *os.File) {
	defer w.fwdWG.Done()
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			w.lastOutput.Store(time.Now().UnixNano())
			dst.Write(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

const colorBoldRed = "\033[1;38;2;255;0;0m"

// watchLoop checks every second whether output has stalled and prints
// a warning to the original stderr (not the intercepted fd, to avoid
// resetting the timer).
func (w *outputWatchdog) watchLoop(ctx context.Context) {
	defer close(w.done)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			last := time.Unix(0, w.lastOutput.Load())
			gap := time.Since(last)
			if gap >= w.threshold {
				step := ""
				if v := w.stepName.Load(); v != nil {
					step, _ = v.(string)
				}
				if step != "" {
					logger.Warn("%s⚠ STALLED: no output for %ds (currently: %s)%s",
						colorBoldRed, int(gap.Seconds()), step, colorReset)
				} else {
					logger.Warn("%s⚠ STALLED: no output for %ds%s",
						colorBoldRed, int(gap.Seconds()), colorReset)
				}
			}
		}
	}
}

// setStep records the name of the currently running build step.
func (w *outputWatchdog) setStep(name string) {
	if w != nil {
		w.stepName.Store(name)
	}
}

// clearStep clears the current step name.
func (w *outputWatchdog) clearStep() {
	if w != nil {
		w.stepName.Store("")
	}
}
