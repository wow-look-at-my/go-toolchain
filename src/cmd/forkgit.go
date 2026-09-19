package cmd

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// forksource.go asks the fork's remote which commit to stand at. These are the
// git helpers it does that with. They were the branch-tracking layer's, and
// that layer went when cmd/go took the job over. The fork submodule is not a
// module dependency, so nothing in cmd/go answers for it.

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

// withGitStderr puts git's own message on the error, which otherwise reports
// only an exit status.
func withGitStderr(err error, stderr []byte) error {
	if err == nil {
		return nil
	}
	if msg := strings.TrimSpace(string(stderr)); msg != "" {
		return fmt.Errorf("%w: %s", err, msg)
	}
	return err
}

// currentBranch answers the branch this checkout stands on, or "" when it is
// detached.
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

// resolveGitURLAndRef finds the repository a module path lives in and returns
// its ls-remote result. Asking for HEAD adds --symref, which also reports the
// branch it points at. Every ref is asked in the same question, so the caller
// learns the default branch and whether a matching branch exists without a
// further round trip. On total failure, the EARLIEST error (the full module
// path) is reported.
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
	branch = eachLsRemoteRef(out, func(h, ref string) { refs[ref] = h })
	return refs, branch
}

// eachLsRemoteRef walks the answer's ref lines and reports the symbolic HEAD's
// branch.
func eachLsRemoteRef(out []byte, fn func(hash, ref string)) (branch string) {
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
			fn(fields[0], "")
		default:
			fn(fields[0], fields[1])
		}
	}
	return branch
}
