package cmd

import (
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// A dependency's generated package is EMPTY until its directive runs: the
// registration lives in the generated file. So this process linked a grammar
// that registers nothing, and every phase after the generate reads an
// extractor with no language in it. Rebuilding is the only cure, because the
// tables enter through the compiler rather than at run time.

// Set on the child, so the rebuild is attempted at most once per run.
const depGenerateReexecEnv = "GO_TOOLCHAIN_DEPGEN_REEXEC"

// reexecAfterDepGenerate hands the run to a build that links what the generate
// step just wrote, and exits with that build's status. It returns when this
// process already reads the generated packages.
func reexecAfterDepGenerate() error {
	if os.Getenv(depGenerateReexecEnv) != "" {
		// The rebuild already happened, and its child reads the tables.
		return nil
	}
	pkg, ok := ownMainPackage()
	if !ok {
		// A consumer carries no source to rebuild from. The comment scan says
		// which languages it could not read, so the gap is never silent.
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
