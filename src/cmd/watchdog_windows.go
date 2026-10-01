//go:build windows

package cmd

import (
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// startWatchdog is a no-op on Windows (dup2 is not available). It says so,
// because a build with no watchdog looks exactly like one that never stalled.
func startWatchdog(threshold time.Duration) *outputWatchdog {
	logger.Warn("watchdog: no stall monitoring this run: windows has no dup2")
	return nil
}

func (w *outputWatchdog) stop() {}
