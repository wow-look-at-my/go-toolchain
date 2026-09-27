package cmd

import (
	"fmt"
	"os"
)

// ciOnlyEnv names the variables that steer which commits a run builds.
var ciOnlyEnv = []string{orgPinEnv, forkCommitEnv}

// CheckCIOnlyEnv fails a run outside GitHub Actions that carries a CI-only
// variable. The run stops, and the variable never reaches a go command.
func CheckCIOnlyEnv() error {
	if isGHA() {
		return nil
	}
	for _, name := range ciOnlyEnv {
		if os.Getenv(name) != "" {
			return fmt.Errorf("%s is set, but only a GitHub Actions run may set it; unset it and run again", name)
		}
	}
	return nil
}
