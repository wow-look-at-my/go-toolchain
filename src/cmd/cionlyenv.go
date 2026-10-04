package cmd

import (
	"fmt"
	"os"
)

// ciOnlyEnv names variables that pin a commit, which no build may set.
var ciOnlyEnv = []string{"GOORGPIN", "GO_TOOLCHAIN_FORK_COMMIT"}

// CheckCIOnlyEnv fails a run that carries a commit pin, in CI or outside it.
// Org dependencies and the fork follow their branch, and fall back to master.
func CheckCIOnlyEnv() error {
	for _, name := range ciOnlyEnv {
		if os.Getenv(name) != "" {
			return fmt.Errorf("%s pins a commit, and builds follow branches only; unset it and run again", name)
		}
	}
	return nil
}
