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

// go/parser and go/types link in from whatever toolchain built THIS binary, and
// the source they read is the fork's. The fork's stdlib uses the fork's own
// language extensions, so a stock-Go build of the pipeline cannot read it: a
// default parameter value in reflect's funcLayout reports "missing ',' in
// parameter list". Both ways into the type-check close together, because the
// fork's export data is a version the vendored importer does not read, which is
// what sends the type-check to source in the first place.
//
// The repair belongs here rather than in whatever invoked the pipeline. One
// command has to be the whole story, so the pipeline rebuilds itself with the
// fork and hands the run to that binary.

// ownModulePath is the module this pipeline's own source lives in.
const ownModulePath = "github.com/wow-look-at-my/go-toolchain"

// reexecGuardEnv marks the child. A rebuild that is somehow still not
// fork-built then says so, rather than rebuilding itself forever.
const reexecGuardEnv = "GO_TOOLCHAIN_FORK_REEXEC"

// builtByFork reports whether the gosmopolitan fork compiled this binary. The
// fork spells itself into its version, which is the only claim available before
// anything runs.
func builtByFork() bool {
	return strings.Contains(runtime.Version(), "cosmo")
}

// builtByForkFunc is the seam. Every gate below this point is unreachable from a
// fork-built test binary, and the test binaries here are all fork-built.
var builtByForkFunc = builtByFork

// reexecUnderFork replaces this run with one the fork compiled, and returns
// with the run still here when that is unnecessary or impossible.
//
// It exits the process on success, with the child's own status: the child did
// the whole build, so there is nothing left for this one to do.
func reexecUnderFork() error {
	if builtByForkFunc() {
		return nil
	}
	if os.Getenv(reexecGuardEnv) != "" {
		// Rebuilding again produces the same binary, so this reports instead.
		return fmt.Errorf("the rebuilt pipeline still reports %s: the `go` that built it is not the fork, so check what %s resolved", runtime.Version(), cosmoGorootEnv)
	}
	pkg, ok := ownMainPackage()
	if !ok {
		// Every published binary is fork-built, so this is a hand build of the
		// pipeline, run somewhere its source is not. There is no degraded mode
		// to offer: the phases that follow read the fork's own source.
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

// ownMainPackage answers this pipeline's main package, and false when the
// working directory is some other module. Only a run inside this module has the
// source to rebuild from.
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

// buildSelfWithFork compiles the pipeline for the host and answers where it
// landed. The target is explicit because the fork builds an APE by default, and
// a shell header is not something exec reads.
func buildSelfWithFork(pkg string) (string, error) {
	// argListTempDir rather than the MkdirTemp default: the path goes into the
	// go command's own argument list, which cosmo does not translate.
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

// hostExeSuffix is what the host needs on an executable's name. NT needs it, and
// a posix host does not care.
func hostExeSuffix() string {
	if hostos.GOOS() == "windows" {
		return ".exe"
	}
	return ""
}
