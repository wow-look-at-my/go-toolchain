package cmd

import (
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Set on the child of a self re-exec.
const selfReexecEnv = "GO_TOOLCHAIN_SELF_REEXEC"

// reexecUnderOwnBuild hands a run of this module to the toolchain it builds.
// Vet and the tests then answer about the compiler that ships, and every
// object they compile is what the build phase and the next run ask for. It
// returns when this binary already reproduces itself.
func reexecUnderOwnBuild() error {
	if os.Getenv(selfReexecEnv) != "" {
		return nil
	}
	pkg, ok := ownMainPackage()
	if !ok {
		return nil
	}
	st := logStep("building the toolchain this run tests with")
	bin, dir, err := buildSelfFixedPoint(pkg)
	if err != nil {
		st.failed()
		return err
	}
	same, err := selfIsFixedPoint(bin)
	if err != nil {
		_ = os.RemoveAll(dir)
		st.failed()
		return err
	}
	st.done()
	if same {
		// The build phase commits this binary instead of building it again.
		fixedPointSelf = bin
		fixedPointDir = dir
		logger.Info("  this binary reproduces itself, so the run stays with it")
		return nil
	}
	code := runSelfWith(bin, selfReexecEnv)
	_ = os.RemoveAll(dir)
	os.Exit(code)
	return nil
}

// fixedPointDir holds fixedPointSelf until the run ends.
var fixedPointDir string

// removeFixedPointSelf deletes the fixed point's directory at the end of the run.
func removeFixedPointSelf() {
	if fixedPointDir == "" {
		return
	}
	_ = os.RemoveAll(fixedPointDir)
	fixedPointDir, fixedPointSelf = "", ""
}

// selfIsFixedPoint reports whether bin and this executable are the same bytes.
func selfIsFixedPoint(bin string) (bool, error) {
	exe, err := selfExecutableFunc()
	if err != nil {
		return false, err
	}
	return sameFile(exe, bin)
}
