//go:build windows

package cmd

import "time"

// startWatchdog is a no-op on Windows (dup2 is not available).
func startWatchdog(threshold time.Duration) *outputWatchdog {
	return nil
}

func (w *outputWatchdog) stop() {}
