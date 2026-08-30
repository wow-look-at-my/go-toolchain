package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/memlimit"
)

// checkDirtyInCI returns an error if running in CI with a dirty working
// tree, so binaries are never shipped built from uncommitted changes.
//
// Excluded: the transient GOMEMLIMIT guard in any state (added, modified,
// deleted -- it is generated for the build and removed after, so it must
// never count), and a branch-tracked pin's version token moving to its
// branch's current commit (a cache of the last resolution, not something a
// human commits). Anything else in go.mod, and any go.sum line for a module
// that did not move, still counts as dirty.
func checkDirtyInCI() error {
	if os.Getenv("CI") == "" {
		return nil
	}
	out, err := exec.Command("git", "status", "--short").Output()
	if err != nil {
		return nil
	}
	files := dirtyFilesExcludingToolchainWrites(string(out))
	if files == "" {
		return nil
	}
	if !jsonOutput {
		logError("", fmt.Sprintf(
			"Working tree is dirty in CI (go-toolchain %s). Dirty files:\n%s\n%s\n"+
				"Fix: run `go-toolchain` locally, review the diff, commit the changes, and push.",
			buildVersion, files, dirtyDiff(files)))
	}
	return fmt.Errorf("working tree is dirty in CI (run `go-toolchain` locally, review the diff, commit, and push)")
}

// dirtyDiff renders what changed. In CI the tree dies with the runner, so this
// is the reader's only look at it.
func dirtyDiff(files string) string { return dirtyDiffIn("", files) }

// dirtyDiffIn runs in dir, or the process directory when dir is empty; taking
// it as a parameter is what spares a test the chdir.
func dirtyDiffIn(dir, files string) string {
	paths := dirtyDiffPaths(files)
	if len(paths) == 0 {
		return ""
	}
	git := func(args ...string) (string, string, error) {
		cmd := exec.Command("git", append(args, paths...)...)
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), strings.TrimSpace(stderr.String()), err
	}
	// Every branch below answers. Silence leaves the reader under an
	// instruction to review a diff that never arrives.
	diff, stderr, err := git("--no-pager", "diff", "--")
	if err != nil {
		return fmt.Sprintf("\nDiff: git diff failed: %v: %s\n", err, stderr)
	}
	if diff == "" {
		// Staged and untracked send the reader to different places.
		staged, _, stagedErr := git("--no-pager", "diff", "--cached", "--")
		if stagedErr != nil || staged == "" {
			return "\nDiff: git reports no content change; the paths are untracked or already committed\n"
		}
		diff = "(staged)\n" + staged
	}
	if lines := strings.Split(diff, "\n"); len(lines) > dirtyDiffMaxLines {
		diff = strings.Join(lines[:dirtyDiffMaxLines], "\n") + "\n... diff truncated"
	}
	return "\nDiff:\n" + diff + "\n"
}

// dirtyDiffMaxLines keeps a runaway diff from burying the message above it.
const dirtyDiffMaxLines = 200

// dirtyDiffPaths reads the paths out of `git status --short` lines. The status
// code sits in front and a rename carries an arrow, so the path is the last
// field either way.
func dirtyDiffPaths(files string) []string {
	var paths []string
	for line := range strings.SplitSeq(files, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			paths = append(paths, fields[len(fields)-1])
		}
	}
	return paths
}

// dirtyFilesExcludingToolchainWrites returns the trimmed `git status --short`
// lines that represent real uncommitted changes, dropping the GOMEMLIMIT
// guard and a branch-tracked pin following its branch. An empty result means
// the tree is clean apart from those.
func dirtyFilesExcludingToolchainWrites(statusOut string) string {
	pins := trackedPinMoves(statusOut)
	var kept []string
	for _, line := range strings.Split(statusOut, "\n") {
		if strings.TrimSpace(line) == "" || statusLineIsToolchainWrite(line, pins) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// statusLineIsToolchainWrite reports whether a `git status --short` porcelain
// line refers to a file this run rewrote on its own authority. The format is
// "XY <path>" (or "XY <old> -> <new>" for renames); the GOMEMLIMIT guard is
// matched by base name so it is ignored in any package directory, while the
// go.mod and go.sum cases are decided per module directory from pins.
func statusLineIsToolchainWrite(line string, pins map[string][]string) bool {
	path := statusLinePath(line)
	if path == "" {
		return false
	}
	if filepath.Base(path) == memlimit.GuardFileName {
		return true
	}
	// A tracked pin's new commit, and the go.sum hashes that follow it. pins
	// holds a directory only when its go.mod changed in no other way.
	if moved, ok := pins[filepath.Dir(path)]; ok {
		switch filepath.Base(path) {
		case "go.mod":
			return true
		case "go.sum":
			return goSumFollowsPins(path, moved)
		}
	}
	// A .gitignore diff that only drops the stale guard line is toolchain migration cleanup, not a developer edit.
	if filepath.Base(path) == ".gitignore" && gitignoreChangeOnlyDropsGuard(path) {
		return true
	}
	return false
}

// statusLinePath extracts the path from a `git status --short` line ("XY
// <path>", or a rename's new name). Returns "" for a line too short to carry a path.
func statusLinePath(line string) string {
	if len(line) < 4 {
		return ""
	}
	path := strings.TrimSpace(line[3:])
	if i := strings.Index(path, " -> "); i != -1 {
		path = path[i+len(" -> "):]
	}
	return strings.Trim(path, "\"")
}

// gitignoreChangeOnlyDropsGuard reports whether the working-tree change to the
// .gitignore at path, relative to HEAD, is solely the removal of the GOMEMLIMIT
// guard line.
func gitignoreChangeOnlyDropsGuard(path string) bool {
	out, err := exec.Command("git", "diff", "HEAD", "--", path).Output()
	if err != nil {
		return false
	}
	return diffOnlyDropsGuard(string(out))
}

// diffOnlyDropsGuard parses a unified diff and reports whether every content
// change is the removal of the guard line: a guard line removed, no
// additions, and nothing else removed (blank-line churn aside). It is split out
// from the git invocation so it can be unit-tested without a repository.
func diffOnlyDropsGuard(diff string) bool {
	sawGuardRemoval := false
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			// file headers, not content
		case strings.HasPrefix(line, "+"):
			if strings.TrimSpace(line[1:]) != "" {
				return false // a real addition
			}
		case strings.HasPrefix(line, "-"):
			content := strings.TrimSpace(line[1:])
			if content == "" {
				continue // removed blank line: cosmetic
			}
			if content != memlimit.GuardFileName {
				return false // removed something other than the guard
			}
			sawGuardRemoval = true
		}
	}
	return sawGuardRemoval
}
