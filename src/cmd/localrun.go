// gosmoRunEnv names the run whose org module heads the fork's go command locks.

package cmd

import (
	"fmt"
	"os"
	"time"
)

// gosmoRunEnv names the run whose org module heads the fork's go command locks.
const gosmoRunEnv = "GOSMOPOLITAN_RUN"

// nameLocalRun gives the go commands of one local build a run of their own.
func nameLocalRun() {
	if os.Getenv("GITHUB_RUN_ID") != "" || os.Getenv(gosmoRunEnv) != "" {
		return
	}
	os.Setenv(gosmoRunEnv, localRunName(os.Getpid(), time.Now()))
}

// localRunName is a run name the fork accepts: owner/repo/run-id/attempt.
func localRunName(pid int, now time.Time) string {
	return fmt.Sprintf("local/go-toolchain/%d-%d/1", pid, now.UnixNano())
}
