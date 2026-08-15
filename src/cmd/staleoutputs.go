package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// A binary in build/ must never outlive the run that produced it.
//
// go-toolchain reports a build through exactly two channels: its output and
// its exit code. Throw both away — pipe stdout and stderr somewhere nobody
// reads, ignore the status — and a binary left at build/<target> by an EARLIER
// successful run is indistinguishable from one this run produced. Executing it
// then "proves" a build that never happened, which is how a red pipeline gets
// reported as done.
//
// So the toolchain deletes the binaries it is responsible for producing:
//
//   - before any phase runs, so a failure at tidy/vet/test/build/dats — or a
//     crash, or a kill — leaves nothing runnable behind;
//   - when the run fails AFTER the build phase already wrote them (a red dats
//     suite, the coverage or warnings gate);
//   - when the agent output guard refuses to run at all. That is the case
//     this exists for: no phase executes, so only the deletion stands between
//     a hidden failure and a false "build successful".
//
// The invariant: build/<target> exists only when the run that wrote it
// finished green. There is deliberately no flag or environment variable to
// turn this off.

// nonBinaryOutputs are files the toolchain writes into the output directory
// that are not build artifacts, even when a target's name is a prefix of
// theirs (a project whose binary is named "wasm" must not lose wasm_exec.js).
var nonBinaryOutputs = set.Of(
	"checksums.txt",
	"wasm_exec.js",
	"profile.json",
	"trace.json",
)

// isOutputArtifact reports whether base — a file name inside the output
// directory — is an artifact go-toolchain produces for the target named name.
// Every shape the toolchain writes is the bare name, the Windows "<name>.exe",
// or "<name>_…": build.BinaryName's <name>_<goos>_<goarch>[.exe], the wasm
// variants, the <name>_cosmo_fat APE, and the <name>_host convenience symlink.
func isOutputArtifact(base, name string) bool {
	// The publish manifest describes the artifacts, so it dies with them: a
	// manifest outliving the binaries it names would send the next publish
	// after a file that is not there.
	if base == buildhostManifestName {
		return true
	}
	if name == "" || nonBinaryOutputs.Contains(base) {
		return false
	}
	return base == name || base == name+".exe" || strings.HasPrefix(base, name+"_")
}

// clearedOutputs records where one module's build artifacts live, so the
// failure path can delete whatever the build phase managed to write — from any
// working directory, since a multi-module run clears each module from that
// module's own directory.
type clearedOutputs struct {
	dir   string   // absolute path of the module's output directory
	names []string // build target names whose artifacts live there
}

var (
	trackedMu      sync.Mutex
	trackedOutputs []clearedOutputs
)

// trackOutputs remembers a module's output directory for discardBuildOutputs.
func trackOutputs(dir string, names []string) {
	trackedMu.Lock()
	defer trackedMu.Unlock()
	for _, c := range trackedOutputs {
		if c.dir == dir {
			return
		}
	}
	trackedOutputs = append(trackedOutputs, clearedOutputs{dir: dir, names: names})
}

// trackedOutputsSnapshot returns a copy of the tracked modules.
func trackedOutputsSnapshot() []clearedOutputs {
	trackedMu.Lock()
	defer trackedMu.Unlock()
	return append([]clearedOutputs(nil), trackedOutputs...)
}

// resetTrackedOutputs clears the tracking state (tests share one process).
func resetTrackedOutputs() {
	trackedMu.Lock()
	defer trackedMu.Unlock()
	trackedOutputs = nil
}

// removeBuildOutputsIn deletes every artifact of the named targets from dir
// and returns what it removed, in directory order. A missing directory is not
// an error; an artifact that exists and cannot be removed is — a binary the
// toolchain fails to delete is exactly the stale binary this prevents.
func removeBuildOutputsIn(dir string, names []string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var removed []string
	for _, e := range entries {
		// Directories are never artifacts. A symlink reports its own type
		// here (never the target's), so stale <name> / <name>_host links are
		// matched and unlinked like any other artifact.
		if e.IsDir() {
			continue
		}
		artifact := false
		for _, name := range names {
			if isOutputArtifact(e.Name(), name) {
				artifact = true
				break
			}
		}
		if !artifact {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if err := os.Remove(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return removed, fmt.Errorf("removing stale build output %s: %w", path, err)
		}
		removed = append(removed, path)
	}
	return removed, nil
}

// moduleOutputTargets resolves the output directory (absolute) and the build
// target names of the module in the current working directory.
func moduleOutputTargets(r runner.CommandRunner) (string, []string, error) {
	targets, err := build.ResolveBuildTargets(r)
	if err != nil {
		return "", nil, err
	}
	dir, err := filepath.Abs(outputDir)
	if err != nil {
		return "", nil, err
	}
	names := make([]string, 0, len(targets))
	for _, t := range targets {
		names = append(names, t.OutputName)
	}
	return dir, names, nil
}

// clearBuildOutputs deletes the current module's build artifacts before the
// pipeline starts, and records the module so a later failure can delete them
// again. From this point the only thing that can put a binary back at
// build/<target> is a build that actually ran.
func clearBuildOutputs(r runner.CommandRunner) error {
	dir, names, err := moduleOutputTargets(r)
	if err != nil {
		return err
	}
	trackOutputs(dir, names)
	removed, err := removeBuildOutputsIn(dir, names)
	if err != nil {
		return err
	}
	for _, path := range removed {
		logger.Debug("⇒ Removed previous build output %s", path)
	}
	return nil
}

// discardBuildOutputs deletes the artifacts of every module cleared this run.
// Called when the pipeline fails after the build phase may already have
// written them: a red run must not leave a runnable binary behind. Best
// effort — the run is already failing, and a removal error must not mask its
// cause.
func discardBuildOutputs() {
	for _, c := range trackedOutputsSnapshot() {
		removed, err := removeBuildOutputsIn(c.dir, c.names)
		for _, path := range removed {
			logger.Info("⇒ Removed %s: the build did not succeed", path)
		}
		if err != nil {
			logger.Info("⇒ Could not remove a build output after the failed run: %v", err)
		}
	}
}

// discardBuildOutputsFromCWD deletes the build artifacts of the module in the
// current directory on an exit path that never entered the pipeline — the
// agent output guard's abort and a failed Go bootstrap. It returns what it
// removed so the caller can say so, and is silent and best-effort: the exit
// message is the priority, and a project with no resolvable targets simply has
// nothing to delete.
func discardBuildOutputsFromCWD() []string {
	dir, names, err := moduleOutputTargets(runner.New())
	if err != nil {
		return nil
	}
	removed, _ := removeBuildOutputsIn(dir, names)
	return removed
}

// DiscardBuildOutputs deletes the build artifacts of the module in the current
// directory. Exported for main's bootstrap-failure exit, which never reaches
// the pipeline: a run that could not even resolve a Go toolchain built
// nothing, so the previous run's binaries must not be left standing as its
// result.
func DiscardBuildOutputs() {
	discardBuildOutputsFromCWD()
}
