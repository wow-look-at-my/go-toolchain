//go:build unix || cosmo

package cmd

import (
	"os"
	"time"
)

// Only the dup2 implementations reach these, and windows has none.

// watchdogDisabled reports the GO_TOOLCHAIN_NO_WATCHDOG off-switch: a fault in fd forwarding can trap all output.
func watchdogDisabled() bool { return os.Getenv("GO_TOOLCHAIN_NO_WATCHDOG") == "1" }

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
