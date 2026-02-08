package cmd

import (
	"io"
	"os"
	"os/exec"
)

// CommandRunner abstracts command execution for testing
type CommandRunner interface {
	Run(name string, args ...string) error
	RunWithOutput(name string, args ...string) ([]byte, error)
	RunWithPipes(name string, args ...string) (stdout io.Reader, wait func() error, err error)
}

// RealCommandRunner executes actual system commands
type RealCommandRunner struct {
	Quiet bool
}

func (r *RealCommandRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if !r.Quiet {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	return cmd.Run()
}

func (r *RealCommandRunner) RunWithOutput(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	return cmd.Output()
}

func (r *RealCommandRunner) RunWithPipes(name string, args ...string) (io.Reader, func() error, error) {
	cmd := exec.Command(name, args...)
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}

	return stdout, cmd.Wait, nil
}

// Default runner for production use
var defaultRunner CommandRunner = &RealCommandRunner{}
