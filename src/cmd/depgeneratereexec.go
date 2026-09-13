package cmd

import (
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Set on the child, so the rebuild runs at most a single time per run.
const depGenerateReexecEnv = "GO_TOOLCHAIN_DEPGEN_REEXEC"

// reexecAfterDepGenerate hands the run to a build that links what the generate
// step just wrote, since the tables enter through the compiler. It exits with
// that build's status, and returns when this process already reads them.
func reexecAfterDepGenerate() error {
	if os.Getenv(depGenerateReexecEnv) != "" {
		// The rebuild already happened, and its child reads the tables.
		return nil
	}
	pkg, ok := ownMainPackage()
	if !ok {
		// A consumer carries no source to rebuild from.
		logger.Warn("⇒ Warning: a dependency generated its output during this run, and this binary predates it: rerun to read what it wrote")
		return nil
	}
	st := logStep("rebuilding the pipeline against the generated output")
	bin, err := buildSelfWithFork(pkg)
	if err != nil {
		st.done()
		return err
	}
	defer func() { _ = os.Remove(bin) }()
	st.done()
	os.Exit(runSelfWith(bin, depGenerateReexecEnv))
	return nil
}
