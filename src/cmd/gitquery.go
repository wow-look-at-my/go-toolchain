// gitquery.go asks git questions for the fork submodule: which commit a ref
// points at, and what this repository's own branch is.
package cmd

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// resolveGitURLAndRef finds the repository a module path lives in and asks it
// about refs. A module path can carry a subdirectory, so each shorter prefix is
// tried until one answers. The output is the raw ls-remote answer, which names
// each ref's commit and the branch a symbolic HEAD points at. Every ref is
// asked in the same question, so a caller pays for one round trip. On total
// failure, the EARLIEST error (the full module path) is reported.
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

// eachLsRemoteRef walks the answer's ref lines and reports the symbolic HEAD's branch.
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

// gitOutput runs a command and returns its stdout. A failure carries the
// command's stderr, which a bare exit status leaves nowhere.
func gitOutput(r runner.CommandRunner, name string, args ...string) ([]byte, error) {
	proc, err := runner.Cmd(name, args...).WithQuiet().Run(r)
	if err != nil {
		return nil, err
	}
	out, _ := io.ReadAll(proc.Stdout())
	stderr, _ := io.ReadAll(proc.Stderr())
	if waitErr := proc.Wait(); waitErr != nil {
		if msg := strings.TrimSpace(string(stderr)); msg != "" {
			return nil, fmt.Errorf("%w: %s", waitErr, msg)
		}
		return nil, waitErr
	}
	return out, nil
}
