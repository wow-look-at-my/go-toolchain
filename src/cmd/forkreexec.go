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

// go/parser links in from whatever built this binary. Depth: docs/CI.md
const ownModulePath = "github.com/wow-look-at-my/go-toolchain"

// Set on the child, so a rebuild that is still not fork-built stops.
const reexecGuardEnv = "GO_TOOLCHAIN_FORK_REEXEC"

// builtByFork reads the fork's name out of its version.
func builtByFork() bool {
	return strings.Contains(runtime.Version(), "cosmo")
}

// The seam: every gate below is unreachable from a fork-built test binary.
var builtByForkFunc = builtByFork

// reexecUnderFork hands this run to a fork-compiled build of the pipeline, and
// exits with that build's status. It returns when nothing needs replacing.
func reexecUnderFork() error {
	if builtByForkFunc() {
		return nil
	}
	if os.Getenv(reexecGuardEnv) != "" {
		return fmt.Errorf("the rebuilt pipeline still reports %s, so the `go` on PATH is not the fork", runtime.Version())
	}
	pkg, ok := ownMainPackage()
	if !ok {
		return fmt.Errorf("%s built this pipeline, and the fork is the only compiler it runs under: rebuild it with `go-toolchain install` from a go-toolchain checkout, or install the published binary", runtime.Version())
	}
	st := logStep("rebuilding the pipeline with the fork")
	bin, err := buildSelfWithFork(pkg)
	if err != nil {
		st.done()
		return err
	}
	defer func() { _ = os.Remove(bin) }()
	st.done()
	os.Exit(runSelf(bin))
	return nil
}

// ownMainPackage is false outside this module: nothing else has the source.
func ownMainPackage() (string, bool) {
	if gomod.ReadModulePath(".") != ownModulePath {
		return "", false
	}
	mains, err := gomod.FindMainPackagesForTarget(".", hostos.GOOS(), runtime.GOARCH)
	if err != nil || len(mains) != 1 {
		return "", false
	}
	return mains[0], true
}

// buildSelfWithFork compiles the pipeline for the HOST: the fork defaults to an
// APE, and exec does not read a shell header.
func buildSelfWithFork(pkg string) (string, error) {
	// The path enters an argument list, which cosmo does not translate.
	dir, err := os.MkdirTemp(argListTempDir(hostos.GOOS()), "go-toolchain-fork-")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "go-toolchain"+hostExeSuffix())
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN=local",
		"GOOS="+hostos.GOOS(),
		"GOARCH="+runtime.GOARCH,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("rebuilding the pipeline with the fork failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return bin, nil
}

// runSelf hands this invocation to bin and answers its exit status.
func runSelf(bin string) int {
	cmd := exec.Command(bin, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), reexecGuardEnv+"=1")
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
