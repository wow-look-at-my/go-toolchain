package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
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

func buildSelfForHost(pkg string) (string, error) {
	// The path enters an argument list, which cosmo does not translate.
	dir, err := os.MkdirTemp(argListTempDir(hostos.GOOS()), "go-toolchain-self-")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "go-toolchain"+hostExeSuffix())
	if err := goBuildHost(pkg, bin); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return bin, nil
}

// goBuildHost compiles pkg as an APE, which runs on this host, with the go
// command this binary is.
func goBuildHost(pkg, bin string) error {
	if len(activeGoCmd) == 0 {
		return fmt.Errorf("no go command is set up for this run: EnsureGoVersion has to run first")
	}
	args := append(append([]string{}, activeGoCmd[1:]...), "build", "-o", bin, pkg)
	cmd := exec.Command(activeGoCmd[0], args...)
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN=local",
		"GOROOT="+activeGoroot,
		"GOOS=cosmo",
		"GOARCH="+runtime.GOARCH,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rebuilding the pipeline failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runSelfWith hands this invocation to bin, with guard set in its
// environment, and answers its exit status.
func runSelfWith(bin, guard string) int {
	cmd := exec.Command(bin, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), guard+"=1")
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
