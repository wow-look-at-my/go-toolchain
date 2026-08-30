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

// Build outputs must never survive a failed run; see CLAUDE.md for why and how deletion is wired into every exit path.

// nonBinaryOutputs are non-artifact files the toolchain writes; a project named "wasm" must not lose wasm_exec.js.
var nonBinaryOutputs = set.Of(
	"checksums.txt",
	"wasm_exec.js",
	"profile.json",
	"trace.json",
)

// isOutputArtifact reports whether base — a file name inside the output
// directory — is an artifact go-toolchain produces for the target named
// name: the bare name, "<name>_…" (goos/goarch variants, wasm, the _host
// symlink), or "<name>.…" (the APE's sidecar ELFs). The ".tmp-"-prefixed
// spelling counts too: the compiler writes its -o there and only a
// successful build moves it onto the final name (build.TmpPrefix), so
// runBuild removes its own temp on failure and these sweeps only ever see
// crash orphans — never an in-flight build's file.
func isOutputArtifact(base, name string) bool {
	// A build's ".tmp-" spelling of an artifact is the same artifact on the
	// floor: the commit never happened, so it must not survive the sweep.
	if rest, ok := strings.CutPrefix(base, build.TmpPrefix); ok {
		return isOutputArtifact(rest, name)
	}
	// The manifest dies with the artifacts it describes, or the next publish targets a file that is gone.
	if base == buildhostManifestName {
		return true
	}
	if name == "" || nonBinaryOutputs.Contains(base) {
		return false
	}
	return base == name || strings.HasPrefix(base, name+"_") || strings.HasPrefix(base, name+".")
}

// clearedOutputs records a module's build-output location, so the failure path can delete it from any cwd.
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

// resetTrackedOutputs clears the tracking state (tests share a process).
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
		// Directories are never artifacts; a symlink reports its own type, so stale <name>/<name>_host links unlink too.
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

// discardBuildOutputsFromCWD deletes the module's artifacts on an exit path that skipped the pipeline (guard abort, failed bootstrap). Best-effort.
func discardBuildOutputsFromCWD() []string {
	dir, names, err := moduleOutputTargets(runner.New())
	if err != nil {
		return nil
	}
	removed, _ := removeBuildOutputsIn(dir, names)
	return removed
}
