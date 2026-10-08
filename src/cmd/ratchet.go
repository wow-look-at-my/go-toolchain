package cmd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// ratchetFile names the command that judges a branch, on its first line that is no comment.
const ratchetFile = ".github/ratchet"

// ratchetByParentEnv is set in a run that reexecuted itself under the build it judges.
const ratchetByParentEnv = "GO_TOOLCHAIN_RATCHET_BY_PARENT"

// checkRatchet runs the default branch's ratchet against this branch.
func checkRatchet() error { return checkRatchetIn("") }

// checkRatchetIn runs the check in dir, or the process directory when dir is
// empty.
//
// The command and everything it reads come from a checkout of the default
// branch, so a branch cannot change what judges it. The command gets the
// branch's checkout as its last argument and fails the run when it exits
// non-zero. A run on the default branch itself passes, because there is
// nothing older to hold it to.
func checkRatchetIn(dir string) error {
	if os.Getenv("CI") == "" || os.Getenv(ratchetByParentEnv) != "" {
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
	head, err := git("rev-parse", "--show-toplevel")
	if err != nil {
		return nil
	}
	head = strings.TrimSpace(head)
	symref, err := git("ls-remote", "--symref", "origin", "HEAD")
	if err != nil {
		return fmt.Errorf("ratchet: reading the default branch: %w", err)
	}
	branch := eachLsRemoteRef([]byte(symref), func(string, string) {})
	if branch == "" {
		return fmt.Errorf("ratchet: origin names no default branch:\n%s", symref)
	}
	if onDefaultBranch(branch) {
		return nil
	}
	if _, err := git("fetch", "--quiet", "--no-tags", "--depth=1", "origin", branch); err != nil {
		return fmt.Errorf("ratchet: fetching %s: %w", branch, err)
	}
	base, err := git("rev-parse", "FETCH_HEAD")
	if err != nil {
		return err
	}
	base = strings.TrimSpace(base)
	if _, err := git("cat-file", "-e", base+":"+ratchetFile); err != nil {
		return nil
	}
	spec, err := git("show", base+":"+ratchetFile)
	if err != nil {
		return err
	}
	argv := ratchetCommand(spec)
	if len(argv) == 0 {
		return fmt.Errorf("ratchet: %s on %s names no command", ratchetFile, branch)
	}

	checkout, err := os.MkdirTemp("", "ratchet-base")
	if err != nil {
		return err
	}
	defer os.RemoveAll(checkout)
	if _, err := git("worktree", "add", "--quiet", "--detach", checkout, base); err != nil {
		return fmt.Errorf("ratchet: checking out %s: %w", branch, err)
	}
	defer git("worktree", "remove", "--force", checkout)
	if err := generateIn(checkout); err != nil {
		return fmt.Errorf("ratchet: generating %s's checkout: %w", branch, err)
	}

	cmd := exec.Command(argv[0], append(argv[1:], head)...)
	cmd.Dir = checkout
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), "GOOS=cosmo", "GOARCH="+runtime.GOARCH)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ratchet: %s's %s (%s) fails on this branch: %w",
			branch, ratchetFile, strings.Join(argv, " "), err)
	}
	return nil
}

// generateIn runs the generate directives that the go.mod at dir approves. A
// command that builds dir then finds each generated file. A directory with no
// go.mod generates nothing.
func generateIn(dir string) error {
	back, err := os.Getwd()
	if err != nil {
		return err
	}
	defer os.Chdir(back)
	if err := os.Chdir(dir); err != nil {
		return err
	}
	f, err := readGoModFile(".")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	hash := ""
	if f.Module != nil {
		hash = parseGenerateMarker(f.Module.Syntax)
	}
	if _, err := satisfyDepGenerate(hash); err != nil {
		return err
	}
	return runGenerate(true, hash)
}

// onDefaultBranch reports whether this CI run is for the default branch.
// GitHub sets GITHUB_REF_NAME to the branch a push run is for.
func onDefaultBranch(branch string) bool {
	return os.Getenv("GITHUB_REF_NAME") == branch
}

// ratchetCommand reads the command off the first line that is neither blank
// nor a comment.
func ratchetCommand(spec string) []string {
	for line := range strings.SplitSeq(spec, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return strings.Fields(line)
	}
	return nil
}
