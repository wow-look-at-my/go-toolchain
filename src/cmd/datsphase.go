package cmd

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// datsSuiteDir is the directory (relative to the module root) where a
// module's .dats command-line test suites live.
const datsSuiteDir = "dats"

// datsBuildDirEnv is exported to the dats child process: a throwaway
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
// removes it) for suites to exec. Copy-then-exec is mandatory: matrix cosmo
// slot artifacts are fat APEs that self-assimilate (rewrite their own file)
// on first exec, so nothing may ever execute a build/ artifact in place. A
// missing source (e.g. a cross-only build with no host-runnable artifact) is
// skipped with a debug log — the suite still runs and fails honestly if it
// needed it.
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
// the working directory read-only and offers no way to declare otherwise. A
// binary that rewrites itself on first exec (a cosmo slot artifact is an APE,
// whose loader does exactly that and exits 121 on a read-only filesystem) must
// therefore be copied into the sandbox's own writable temp space by the suite
// that runs it -- one `cp` into `$(mktemp -d)`, which is what dats/README.md
// tells suites to do. Do not "fix" any of this by passing --no-sandbox: that
// turns the isolation off for every suite command in every consuming repo.
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
		}
	}
	return dir, nil
}

// runDatsPhase runs the module's dats suites (if any) against the binaries
// just built. The dats binary is resolved (env override or buildhost
// download) only after the suite-presence gate passes, so modules without a
// dats/ directory pay zero network and see zero output. dats itself always
// runs every discovered test — there is deliberately no filtering, selection,
// or skip mechanism at either layer. Failures fail the build.
func runDatsPhase(r runner.CommandRunner, quiet bool, artifacts []datsArtifact) error {
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

	datsBin, err := ensureDatsFunc()
	if err != nil {
		return fail(err)
	}

	buildDir, err := stageDatsArtifacts(artifacts)
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(buildDir)

	// Serial on purpose (no -j): the report stays byte-deterministic, and
	// staged APE copies never race their first-exec self-assimilation.
	// GOCACHEPROG and GOCACHE_STATS_SOCK are cleared so a suite command that
	// runs `go ...` cannot spawn cacheprog children of THIS binary against
	// the outer daemon (stats pollution, stdout pipe stalls) — the same
	// clearing the bench runner and embeddedFiles do.
	cmd := runner.Cmd(datsBin, "test", datsSuiteDir).
		WithEnv(datsBuildDirEnv, buildDir).
		WithEnv("GOCACHEPROG", "").
		WithEnv("GOCACHE_STATS_SOCK", "")
	if st != nil {
		cmd = cmd.WithOnFirstOutput(st.noteOutput)
	}
	if quiet {
		// --json mode: stdout carries the JSON coverage payload. Route the
		// dats report to stderr so it stays visible without corrupting it.
		cmd.StdoutWriter = os.Stderr
	}
	proc, err := cmd.Run(r)
	if err != nil {
		return fail(err)
	}
	if err := proc.Wait(); err != nil {
		return fail(err)
	}
	if st != nil {
		st.done()
	}
	return nil
}
