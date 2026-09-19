package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// checkDirtyInCI returns an error if running in CI with a dirty working
// tree, so binaries are never shipped built from uncommitted changes.
func checkDirtyInCI() error {
	if os.Getenv("CI") == "" {
		return nil
	}
	refreshGitIndex("")
	out, err := exec.Command("git", "status", "--short").Output()
	if err != nil {
		return nil
	}
	files := dropForkGitlink(strings.TrimSpace(string(out)))
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

// dropForkGitlink removes the fork submodule's own status line. syncForkSource
// puts the checkout on the fork branch named like this, or on its default
// branch, so the gitlink moves whenever the fork does. That is the build
// following the fork, never an uncommitted change, and the recorded commit is
// only a starting point for the earliest clone. Every other dirty path survives.
func dropForkGitlink(files string) string {
	var kept []string
	for line := range strings.SplitSeq(files, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if isForkSubmodulePath(fields[len(fields)-1]) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// refreshGitIndex re-stats the tracked files and drops the modified mark from
// any whose content still matches. git status trusts that stat cache, so a
// rewrite producing the same bytes otherwise reads as an edit. A real change
// survives the refresh, which is why this only removes false alarms.
// It returns what git said about the paths that did change.
func refreshGitIndex(dir string) string {
	cmd := exec.Command("git", "update-index", "--refresh")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	// git reports "needs update" through a failing exit status, as usual.
	out, _ := cmd.Output()
	return strings.TrimSpace(strings.TrimSpace(string(out)) + "\n" + strings.TrimSpace(stderr.String()))
}

// dirtyDiff renders what changed; in CI the tree dies with the runner.
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
	// Every branch below answers; silence is what taught nobody anything.
	diff, stderr, err := git("--no-pager", "diff", "--")
	if err != nil {
		return fmt.Sprintf("\nDiff: git diff failed: %v: %s\n", err, stderr)
	}
	if diff == "" {
		// Staged and untracked send the reader to different places.
		staged, _, stagedErr := git("--no-pager", "diff", "--cached", "--")
		if stagedErr != nil || staged == "" {
			return noContentChangeReport(dir)
		}
		diff = "(staged)\n" + staged
	}
	if lines := strings.Split(diff, "\n"); len(lines) > dirtyDiffMaxLines {
		diff = strings.Join(lines[:dirtyDiffMaxLines], "\n") + "\n... diff truncated"
	}
	return "\nDiff:\n" + diff + "\n"
}

// noContentChangeReport covers the case where the paths are untracked or
// already committed, and the case where status and diff disagree, which is a
// stat cache the refresh could not settle. Naming both beats a shrug.
func noContentChangeReport(dir string) string {
	refresh := refreshGitIndex(dir)
	if refresh == "" {
		return "\nDiff: git reports no content change; the paths are untracked or already committed\n"
	}
	return "\nDiff: git status and git diff disagree. Index refresh says:\n" + refresh + "\n"
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
