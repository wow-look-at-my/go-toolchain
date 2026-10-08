package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// protectedPathsFile names the paths, one per line.
const protectedPathsFile = ".github/protected-paths"

// checkProtectedPaths fails a CI run on a branch whose copy of a protected path differs from the default branch.
func checkProtectedPaths() error { return checkProtectedPathsIn("") }

// checkProtectedPathsIn runs the check in dir, or the process directory when
// dir is empty.
//
// The list comes from the default branch, never from the branch under test, so
// a branch cannot shorten it. A run on the default branch itself passes: that
// is where a change to a protected path lands once its owner merges it.
func checkProtectedPathsIn(dir string) error {
	if os.Getenv("CI") == "" {
		return nil
	}
	git := func(args ...string) (string, error) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return string(out), nil
	}

	// A directory outside any repository has no branch to compare.
	if _, err := git("rev-parse", "--is-inside-work-tree"); err != nil {
		return nil
	}
	symref, err := git("ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return fmt.Errorf("protected paths: reading the default branch: %w", err)
	}
	branch := eachLsRemoteRef([]byte(symref), func(string, string) {})
	if branch == "" {
		return fmt.Errorf("protected paths: origin names no default branch:\n%s", symref)
	}
	if onDefaultBranch(branch) {
		return nil
	}
	if _, err := git("fetch", "--quiet", "--no-tags", "--depth=1", "origin", branch); err != nil {
		return fmt.Errorf("protected paths: fetching %s: %w", branch, err)
	}
	base, err := git("rev-parse", "FETCH_HEAD")
	if err != nil {
		return err
	}
	base = strings.TrimSpace(base)

	if _, err := git("cat-file", "-e", base+":"+protectedPathsFile); err != nil {
		return nil
	}
	list, err := git("show", base+":"+protectedPathsFile)
	if err != nil {
		return err
	}
	paths := parseProtectedPaths(list)
	if len(paths) == 0 {
		return nil
	}

	changed, err := git(append([]string{"diff", "--name-only", base, "HEAD", "--"}, paths...)...)
	if err != nil {
		return err
	}
	changed = strings.TrimSpace(changed)
	if changed == "" {
		return nil
	}
	return fmt.Errorf("protected paths: this branch changes paths %s protects on %s:\n%s\n"+
		"A branch may not change them, because they define what the repository guarantees. "+
		"Restore them with `git checkout origin/%s -- %s`. Only the repository owner merges a change to them.",
		protectedPathsFile, branch, changed, branch, strings.Join(paths, " "))
}

// onDefaultBranch reports whether this CI run is for the default branch.
// GitHub sets GITHUB_REF_NAME to the branch a push run is for.
func onDefaultBranch(branch string) bool {
	return os.Getenv("GITHUB_REF_NAME") == branch
}

// parseProtectedPaths reads the list, skipping blank lines and comments.
func parseProtectedPaths(list string) []string {
	var paths []string
	for line := range strings.SplitSeq(list, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		paths = append(paths, line)
	}
	return paths
}
