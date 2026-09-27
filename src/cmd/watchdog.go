package cmd

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// activeWatchdog is the current output watchdog, if any; the step system reads it to report step names.
var activeWatchdog *outputWatchdog

// outputWatchdog monitors all stdout/stderr output and warns when the build
// goes silent for too long. It intercepts the stdout and stderr descriptors via dup2
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

const colorBoldRed = "\033[1;38;2;255;0;0m"

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
