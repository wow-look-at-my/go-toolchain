package cmd

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// gitOutput runs a command and returns its stdout.
func gitOutput(r runner.CommandRunner, name string, args ...string) ([]byte, error) {
	proc, err := runner.Cmd(name, args...).WithQuiet().Run(r)
	if err != nil {
		return nil, err
	}
	out, _ := io.ReadAll(proc.Stdout())
	stderr, _ := io.ReadAll(proc.Stderr())
	if err := withGitStderr(proc.Wait(), stderr); err != nil {
		return nil, err
	}
	return out, nil
}

// withGitStderr attaches what git said to a failure. WithQuiet() sends stderr
// nowhere, so a bare exit status was the whole report.
func withGitStderr(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

// currentBranch is the branch this repository is on, empty when there is none:
// a detached HEAD, or no repository at all.
func currentBranch(r runner.CommandRunner) string {
	out, err := gitOutput(r, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	if branch := strings.TrimSpace(string(out)); branch != "HEAD" {
		return branch
	}
	return ""
}

// resolveGitURLAndRef asks a module's repository about refs, and returns the
// URL that answered along with the raw ls-remote output. It tries the full
// module path as the git URL, then backs off a path segment at a time, which
// reaches a module in a subdirectory of its repository on any host with no
// table of hosts. Backoff triggers on a git-level failure, never on an empty
// answer from a reachable remote. A request for HEAD adds --symref, so the
// answer also names the branch HEAD points at. On total failure the earliest
// error, from the full module path, is the error reported.
func resolveGitURLAndRef(r runner.CommandRunner, mod string, refs ...string) (gitURL string, output []byte, err error) {
	parts := strings.Split(mod, "/")
	var firstErr error
	keep := func(e error) {
		if firstErr == nil {
			firstErr = e
		}
	}
	for i := len(parts); i >= 2; i-- {
		url := "https://" + strings.Join(parts[:i], "/")
		args := []string{"ls-remote"}
		if slices.Contains(refs, "HEAD") {
			args = append(args, "--symref")
		}
		args = append(append(args, url), refs...)
		proc, runErr := runner.Cmd("git", args...).WithQuiet().Run(r)
		if runErr != nil {
			keep(fmt.Errorf("git ls-remote %s failed: %w", url, runErr))
			continue
		}
		out, _ := io.ReadAll(proc.Stdout())
		if waitErr := proc.Wait(); waitErr != nil {
			keep(fmt.Errorf("git ls-remote %s failed: %w", url, waitErr))
			continue
		}
		return url, out, nil
	}
	return "", nil, firstErr
}

// parseLsRemoteRefs reads an answer covering several refs: each ref's commit
// by its full name, and the branch a symbolic HEAD resolves to.
func parseLsRemoteRefs(out []byte) (refs map[string]string, branch string) {
	refs = map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "ref: "); ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				branch = strings.TrimPrefix(fields[0], "refs/heads/")
			}
			continue
		}
		switch fields := strings.Fields(line); len(fields) {
		case 0:
		case 1:
			refs[""] = fields[0]
		default:
			refs[fields[1]] = fields[0]
		}
	}
	return refs, branch
}
