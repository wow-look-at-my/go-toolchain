package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	dats "github.com/wow-look-at-my/dats"
	datsrunner "github.com/wow-look-at-my/dats/runner"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// datsSuiteDir is the dats suites directory, relative to the module root.
const datsSuiteDir = "dats"

// datsRunFunc is dats.Run, linked in rather than downloaded and exec'd. Swapped in tests.
var datsRunFunc = dats.Run

// datsBuildDirEnv names the env var pointing suite commands at the staged binaries dir.
const datsBuildDirEnv = "GO_TOOLCHAIN_DATS_BUILD_DIR"

// datsArtifact names a built binary to hand to dats suites.
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

// hasDatsSuites reports whether dir has any non-hidden *.dats files under
// dats/, recursively, skipping hidden directories. The walk mirrors dats'
// own discovery, so this gate never no-ops a suite dats would run. A missing
// or non-directory dats/ falls out naturally: found stays false.
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

// datsStageDir is where built binaries are staged for dats, under the gitignored build output dir.
const datsStageDir = ".dats-stage"

// stageDatsArtifacts copies built binaries into a staging dir (caller
// removes it) for suites to exec. Copy-then-exec is mandatory: cosmo fat
// APEs self-assimilate on their earliest exec, so nothing may run a build/ artifact
// in place.
//
// The dir must sit INSIDE the module root, as an absolute path: dats
// sandboxes every command, reaching only the working directory (read-only)
// plus declared paths.
//
// Staged binaries are READ-ONLY. A self-rewriting binary (the cosmo APE)
// must be copied to the sandbox's own writable temp space by the suite that
// runs it (`cp` into `$(mktemp -d)`, per dats/README.md).
func stageDatsArtifacts(artifacts []datsArtifact) (string, error) {
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, outputDir, datsStageDir)
	// A killed run leaves the dir behind; start from a clean directory.
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
			// Force the exec bit in case copyFile staged from a mode-stripped source.
			copyErr = os.Chmod(dst, 0o755)
		}
		if copyErr != nil {
			logger.Debug("dats: not staging %s: %v", a.sourcePath, copyErr)
		}
	}
	return dir, nil
}

// noteFirstWrite calls note a single time, on the earliest byte written to w, to end
// the step's "..." line at the right moment.
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

// runDatsOnly runs dats suites for a repo with dats/ but no go.mod. Nothing
// was built, so $GO_TOOLCHAIN_DATS_BUILD_DIR is an empty directory. Staging
// still uses the build output dir (the sandbox exposes only the working
// directory); the empty parent is removed after, leaving no stray build/.
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

// datsSandboxProbe resolves dats' auto backend. Swapped in tests.
var datsSandboxProbe = func() error {
	_, err := datsrunner.NewSandboxConfig(datsrunner.SandboxAuto, "").Backend()
	return err
}

// datsSandbox picks the isolation the suites run under: auto wherever a
// backend can exist, the host where none can. Refusing on an NT host would
// take the suites away from the host they exist to cover, so the phase says
// what it lost and runs them. A missing bwrap on linux is fixable, carries no
// marker, and stays fatal.
func datsSandbox() dats.Sandbox {
	err := datsSandboxProbe()
	if err == nil || !errors.Is(err, datsrunner.ErrNoBackendOnHost) {
		return dats.Sandbox{} // auto, and a fixable gap still fails the run
	}
	logger.Error("dats suites run UNSANDBOXED on this host: %v", err)
	logger.Error("every suite still runs and every assertion still holds; what is gone is the isolation between a command and this machine")
	return dats.Sandbox{Mode: datsrunner.SandboxNone}
}

// runDatsPhase runs the module's dats suites (if any) against the binaries
// just built, in this process: go-toolchain links the dats library, so the
// suite-presence gate is the only thing standing between a module and its
// suites — no download, no cache, no dats version to drift from the linked-in copy.
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

	// stdout normally, or stderr under --json (stdout carries the payload). Held writer; see logging.go.
	out := rawStdout
	if quiet {
		out = rawStderr
	}
	if st != nil {
		out = &noteFirstWrite{w: out, note: st.noteOutput}
	}

	// Serial, so staged APE copies never race their self-assimilation.
	// GO_BUILDCACHE_CONFIG is cleared so a suite's `go` cannot reach the
	// outer shared cache -- gosmopolitan's cmd/go consults it directly.
	res, err := datsRunFunc(context.Background(), dats.Options{
		Paths:   []string{datsSuiteDir},
		Output:  out,
		Sandbox: datsSandbox(),
		Env: []string{
			datsBuildDirEnv + "=" + buildDir,
			"GO_BUILDCACHE_CONFIG=",
		},
	})
	if err != nil {
		return fail(err)
	}
	if !res.Ok() {
		// dats already printed which tests failed and why; this line fails the build.
		return fail(fmt.Errorf("%d of %d tests failed", res.Failed, res.Passed+res.Failed))
	}
	if st != nil {
		st.done()
	}
	return nil
}
