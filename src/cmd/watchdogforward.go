//go:build unix || cosmo

package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Only the dup2 implementations reach these, and windows has none.

// watchdogOff reports that no stall monitoring is running, and why. Every
// startWatchdog path that gives up takes this: a build with no watchdog looks
// exactly like a build that never stalled, so the absence has to be stated or
// a silent phase is read as a fast one.
func watchdogOff(reason string, args ...any) *outputWatchdog {
	logger.Warn("watchdog: no stall monitoring this run: "+reason, args...)
	return nil
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

// watchLoop checks on a fixed tick whether output has stalled and prints
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
				// Must write to origStderr, never the logger: the logger writes stderr, the watchdog's own
				// monitored pipe, which would reset the stall timer or get lost in a trapped pipe. Writing to a
				// variable-held writer keeps this bannedoutput-clean.
				if step != "" {
					fmt.Fprintf(w.origStderr, "%s⚠ STALLED: no output for %ds (currently: %s)%s\n",
						colorBoldRed, int(gap.Seconds()), step, colorReset)
				} else {
					fmt.Fprintf(w.origStderr, "%s⚠ STALLED: no output for %ds%s\n",
						colorBoldRed, int(gap.Seconds()), colorReset)
				}
			}
		}
	}
}
