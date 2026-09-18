// gitrun.go runs a git command and reports what it said.
//
// The fork checkout in forksource.go drives git, and a bare exit status is a
// poor report when the failure is a network or a ref that is not there.
package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// gitOutput runs a command and returns its stdout.
func gitOutput(r runner.CommandRunner, name string, args ...string) ([]byte, error) {
	proc, err := runner.Cmd(name, args...).WithQuiet().Run(r)
	if err != nil {
		return nil, err
	}
	out, _ := io.ReadAll(proc.Stdout())
	stderr, _ := io.ReadAll(proc.Stderr())
	if err := withGitStderr(proc.Wait(), stderr); err != nil {
		return nil, err
	}
	return out, nil
}

// withGitStderr attaches what git said to a failure. WithQuiet() sends stderr
// nowhere, so a bare exit status was the whole report.
func withGitStderr(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}
