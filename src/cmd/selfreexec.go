package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// The pipeline runs only as a build of the active toolchain's front end. Depth: docs/CI.md
const ownModulePath = "github.com/wow-look-at-my/go-toolchain"

// ownMainPackage is false outside this module, where the source comes from a clone.
func ownMainPackage() (string, bool) {
	if gomod.ReadModulePath(".") != ownModulePath {
		return "", false
	}
	mains, err := gomod.FindMainPackagesForTarget(".", "cosmo", runtime.GOARCH)
	if err != nil || len(mains) != 1 {
		return "", false
	}
	return mains[0], true
}

// buildSelfFixedPoint builds pkg the way the build phase does, in passes
// until a binary reproduces itself, and answers that binary and the
// directory holding it, which the caller removes.
func buildSelfFixedPoint(pkg string) (bin, dir string, err error) {
	env, err := resolveForkBuildEnv(true)
	if err != nil {
		return "", "", err
	}
	// The path enters an argument list, which cosmo does not translate.
	dir, err = os.MkdirTemp(argListTempDir(hostos.GOOS()), "go-toolchain-self-")
	if err != nil {
		return "", "", err
	}
	job := env.apeJob(pkg, filepath.Join(dir, "go-toolchain"+hostExeSuffix()))
	bin, err = buildSelfPasses(runner.New(), job, dir, nil)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", "", fmt.Errorf("rebuilding the pipeline failed: %w", err)
	}
	return bin, dir, nil
}

// runSelfWith hands this invocation to bin, with each guard set in its
// environment, and answers its exit status.
func runSelfWith(bin string, guards ...string) int {
	cmd := exec.Command(bin, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	for _, guard := range guards {
		cmd.Env = append(cmd.Env, guard+"=1")
	}
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		logger.Error("⇒ the rebuilt pipeline at %s will not run: %v", bin, err)
		return 1
	}
	return 0
}

// hostExeSuffix reads the HOST: NT needs it, and the compile target does not say.
func hostExeSuffix() string {
	if hostos.GOOS() == "windows" {
		return ".exe"
	}
	return ""
}
