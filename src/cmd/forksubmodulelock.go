// forkSubmoduleLockPrefix starts the run lock name of a fork submodule.

package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// forkSubmoduleLockPrefix starts the run lock name of a fork submodule.
const forkSubmoduleLockPrefix = "commit:"

// forkSubmodule is one org submodule of the fork checkout, on the branch it follows.
type forkSubmodule struct {
	path   string
	module string
	branch string
}

// lockForkSubmodules puts each org submodule of the fork at the commit this
// run attempt locked for its branch. The fork's script moves each to its
// branch head, and a commit that lands mid-run must reach no job of the run.
func lockForkSubmodules(r runner.CommandRunner) error {
	subs, err := forkOrgSubmodules(r)
	if err != nil {
		return err
	}
	for _, sub := range subs {
		dir := filepath.Join(forkSubmoduleDir, filepath.FromSlash(sub.path))
		out, err := gitOutput(r, "git", "-C", dir, "rev-parse", "HEAD")
		if err != nil {
			return fmt.Errorf("reading the commit of %s: %w", dir, err)
		}
		head := strings.TrimSpace(string(out))
		commit, err := lockedRunValue(forkSubmoduleLockPrefix+sub.module+"@"+sub.branch, head)
		if err != nil {
			return err
		}
		if commit != head {
			if err := checkoutLockedSubmodule(r, dir, commit); err != nil {
				return err
			}
		}
		logger.Info("gosmopolitan: %s at %s %s, locked for this run", sub.path, sub.branch, commit)
	}
	return nil
}

// checkoutLockedSubmodule detaches dir at commit and checks out its own submodules.
func checkoutLockedSubmodule(r runner.CommandRunner, dir, commit string) error {
	for _, args := range [][]string{
		{"-C", dir, "fetch", "--quiet", "--depth=1", "origin", commit},
		{"-C", dir, "checkout", "--quiet", "--detach", commit},
		{"-C", dir, "submodule", "update", "--init", "--recursive"},
	} {
		if _, err := gitOutput(r, "git", args...); err != nil {
			return fmt.Errorf("putting %s at %s, the commit this run locked: %w", dir, commit, err)
		}
	}
	return nil
}

// forkOrgSubmodules lists the fork's org submodules that follow a branch. The
// fork's script records that branch in the checkout's config. A submodule with
// none stays at its gitlink, which no head can move.
func forkOrgSubmodules(r runner.CommandRunner) ([]forkSubmodule, error) {
	if _, err := os.Stat(filepath.Join(forkSubmoduleDir, ".gitmodules")); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	out, err := gitOutput(r, "git", "-C", forkSubmoduleDir, "config", "-f", ".gitmodules", "--get-regexp", `^submodule\..*\.path$`)
	if err != nil {
		return nil, fmt.Errorf("listing the fork's submodules: %w", err)
	}
	var subs []forkSubmodule
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		key, path, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(key, "submodule."), ".path")
		url, err := gitOutput(r, "git", "-C", forkSubmoduleDir, "config", "-f", ".gitmodules", "--get", "submodule."+name+".url")
		if err != nil {
			return nil, fmt.Errorf("reading the URL of the fork's %s submodule: %w", name, err)
		}
		module, ok := orgModuleOfURL(strings.TrimSpace(string(url)))
		if !ok {
			continue
		}
		branch, err := gitOutput(r, "git", "-C", forkSubmoduleDir, "config", "--get", "submodule."+name+".branch")
		if err != nil || strings.TrimSpace(string(branch)) == "" {
			continue
		}
		subs = append(subs, forkSubmodule{path: path, module: module, branch: strings.TrimSpace(string(branch))})
	}
	return subs, nil
}

// orgModuleOfURL answers the repository path of an org URL, as the fork's
// script picks its submodules.
func orgModuleOfURL(url string) (string, bool) {
	_, rest, ok := strings.Cut(url, "github.com/wow-look-at-my/")
	if !ok {
		return "", false
	}
	return "github.com/wow-look-at-my/" + strings.TrimSuffix(strings.TrimSuffix(rest, "/"), ".git"), true
}
