package cmd

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	dats "github.com/wow-look-at-my/dats"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// datsSuiteDir is the directory (relative to the module root) where a
// module's .dats command-line test suites live.
const datsSuiteDir = "dats"

// datsRunFunc is the dats library entry point. dats is LINKED IN, not
// downloaded and exec'd: the suites run in this process, so there is no
// binary to resolve, no version to keep in step with, and no host that can
// be missing one. Swapped in tests.
var datsRunFunc = dats.Run

// datsBuildDirEnv is handed to every suite command: a throwaway
// directory holding copies of the module's built binaries, named by their
// bare output name (plus .exe on windows hosts). Suites exec the binaries
// through it, e.g. `"$GO_TOOLCHAIN_DATS_BUILD_DIR/mytool" --help`.
const datsBuildDirEnv = "GO_TOOLCHAIN_DATS_BUILD_DIR"

// datsArtifact names one built binary to hand to dats suites.
type datsArtifact struct {
	sourcePath string // the built artifact (build/<name>, build/<name>_<os>_<arch>, ...)
	name       string // bare name exposed in the handoff dir (see datsArtifactName)
}

// datsArtifactName is the name a built binary is exposed under in the dats
// handoff dir: the bare output name, plus .exe on windows hosts.
func datsArtifactName(outputName, goos string) string {
	if goos == "windows" {
		return outputName + ".exe"
	}
	return outputName
}

// hasDatsSuites reports whether the module rooted at dir has any dats suites:
// non-hidden *.dats files under dats/, recursively, skipping hidden
// directories. The walk mirrors dats' own discovery (which skips hidden
// entries but nothing else), so this gate can never no-op a suite dats would
// run — it only decides run-everything vs. nothing-to-run. A missing dats/
// (or one that is a plain file) falls out naturally: the walk errors or
// visits a single non-.dats file, and found stays false.
func hasDatsSuites(dir string) bool {
	root := filepath.Join(dir, datsSuiteDir)
	found := false
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".dats") {
			found = true
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// datsStageDir is the staging directory the built binaries are copied into,
// relative to the module root. It lives UNDER the build output dir, which
// go-toolchain keeps gitignored in every repo it builds (see
// ensureBuildDirInGitignore), so staging never dirties the tree.
const datsStageDir = ".dats-stage"

// stageDatsArtifacts copies built binaries into a staging dir (the caller
// removes it) for suites to exec. It stages copies because a matrix cosmo slot
// artifact is a fat APE that rewrites its own file the first time it runs, so
// nothing may ever execute a build/ artifact in place. A missing source (e.g. a
// cross-only build with no host-runnable artifact) is skipped with a debug log
// — the suite still runs and fails honestly if it needed it.
//
// The staging dir must be INSIDE the module root, and the path handed to dats
// absolute. dats sandboxes every test command, and of the host a sandboxed
// command reaches only the working directory (mounted read-only) plus the
// paths the run declares — a staging dir under $TMPDIR is invisible to every
// backend, and every suite fails its setup command instead of running. An
// absolute path under the working directory resolves identically inside and
// outside all three backends.
//
// The staged binaries are READABLE there, not writable -- the sandbox exposes
// the working directory read-only and offers no way to declare otherwise. So
// staging assimilates each APE copy here, while the file is still writable:
// the loader's first-exec self-rewrite needs write access and exits 121
// without it, and a suite must be able to exec the handoff path directly. A
// suite copies a binary only when its own test needs a writable one. Do not
// "fix" an exec failure by passing --no-sandbox: that turns the isolation off
// for every suite command in every consuming repo.
func stageDatsArtifacts(artifacts []datsArtifact) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, outputDir, datsStageDir)
	// A killed run leaves the dir behind; start from a clean one.
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	for _, a := range artifacts {
		dst := filepath.Join(dir, a.name)
		copyErr := copyFile(a.sourcePath, dst)
		if copyErr == nil {
			// copyFile propagates the source mode; force the exec bit in
			// case the artifact was staged from a mode-stripped filesystem.
			copyErr = os.Chmod(dst, 0o755)
		}
		if copyErr != nil {
			logger.Debug("dats: not staging %s: %v", a.sourcePath, copyErr)
			continue
		}
		// Loud, and not fatal: the copy still runs anywhere it is writable,
		// so a suite that copies it keeps passing while this says why one
		// that execs the handoff path directly will not.
		if err := assimilateAPE(dst); err != nil {
			logger.Warn("dats: %s stays an APE and a suite cannot exec it from the read-only handoff dir: %v", a.name, err)
		}
	}
	return dir, nil
}

// noteFirstWrite calls note once, before the first byte reaches w. It is how
// the in-process dats report terminates the step's "..." line at exactly the
// moment a subprocess's first output used to.
type noteFirstWrite struct {
	w    io.Writer
	note func()
	once sync.Once
}

func (n *noteFirstWrite) Write(p []byte) (int, error) {
	if len(p) > 0 {
		n.once.Do(n.note)
	}
	return n.w.Write(p)
}

// runDatsOnly is the whole run for a repo that has dats suites but no go.mod:
// a shell or TypeScript project whose CLI is still worth testing this way.
// Refusing those outright pushed them into fetching a standalone dats binary
// and wiring a CI step by hand -- work this toolchain already does, and a
// version skew waiting to happen, since the dats linked in here is the one
// that would drift from it.
//
// Nothing was built, so there are no artifacts to hand over: such a suite
// exercises what is already in the tree, and $GO_TOOLCHAIN_DATS_BUILD_DIR is
// an empty directory rather than a missing one.
//
// Staging still goes under the build output dir, because the sandbox exposes
// only the working directory -- but the empty parent is removed afterwards, so
// a non-Go repo is not left holding a stray build/ it never asked for and does
// not gitignore.
func runDatsOnly() error {
	logger.Info("⇒ No go.mod; running dats suites only")

	_, statErr := os.Stat(outputDir)
	preexisting := statErr == nil

	err := runDatsPhase(false, nil)

	if !preexisting {
		if entries, readErr := os.ReadDir(outputDir); readErr == nil && len(entries) == 0 {
			os.Remove(outputDir)
		}
	}
	return err
}

// runDatsPhase runs the module's dats suites (if any) against the binaries
// just built, in this process: go-toolchain links the dats library, so the
// suite-presence gate is the only thing standing between a module and its
// suites — no download, no cache, no dats version to drift from this one.
// Modules without a dats/ directory pay nothing and print nothing.
//
// dats itself always runs every discovered test — there is deliberately no
// filtering, selection, or skip mechanism at either layer. Failures fail the
// build.
func runDatsPhase(quiet bool, artifacts []datsArtifact) error {
	if !hasDatsSuites(".") {
		return nil
	}

	var st *step
	if !quiet {
		st = logStep("Running dats suites")
	}
	fail := func(err error) error {
		if st != nil {
			st.failed()
		}
		return fmt.Errorf("dats suites failed: %w", err)
	}

	buildDir, err := stageDatsArtifacts(artifacts)
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(buildDir)

	// The report goes to stdout, except under --json where stdout carries the
	// JSON coverage payload — then it goes to stderr so it stays visible
	// without corrupting it. Held writers, deliberately (see logging.go).
	out := rawStdout
	if quiet {
		out = rawStderr
	}
	if st != nil {
		out = &noteFirstWrite{w: out, note: st.noteOutput}
	}

	// Jobs stays 0 (serial) on purpose: the report is byte-deterministic. The
	// sandbox is dats' default (auto) — whether a suite needs the host is the
	// SUITE's declaration to make, never the toolchain's.
	//
	// GOCACHEPROG and GOCACHE_STATS_SOCK are cleared for every suite command
	// so one that runs `go ...` cannot spawn cacheprog children of THIS binary
	// against the outer daemon (stats pollution, stdout pipe stalls) — the
	// same clearing the bench runner and embeddedFiles do.
	res, err := datsRunFunc(context.Background(), dats.Options{
		Paths:  []string{datsSuiteDir},
		Output: out,
		Env: []string{
			datsBuildDirEnv + "=" + buildDir,
			"GOCACHEPROG=",
			"GOCACHE_STATS_SOCK=",
		},
	})
	if err != nil {
		return fail(err)
	}
	if !res.Ok() {
		// dats already printed which tests failed and why; this is the line
		// that fails the build.
		return fail(fmt.Errorf("%d of %d tests failed", res.Failed, res.Passed+res.Failed))
	}
	if st != nil {
		st.done()
	}
	return nil
}
