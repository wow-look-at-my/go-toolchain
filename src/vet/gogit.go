//go:build !cosmo

package vet

import (
	"fmt"
	"path/filepath"

	git "github.com/go-git/go-git/v5"
)

// This file carries the package's only go-git import. GOOS=cosmo builds
// exclude it — go-git's go-billy/osfs matches cosmo's `unix` build tag but
// depends on golang.org/x/sys/unix, which has no cosmo port — and
// gogit_cosmo.go stubs checkFileCommittedGoGit so the caller
// (checkFileCommittedByName) always takes its git-CLI fallback there.

// checkFileCommittedGoGit checks file status using the go-git library.
func checkFileCommittedGoGit(filename string) error {
	fileDir := filepath.Dir(filename)
	repo, err := git.PlainOpenWithOptions(fileDir, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return fmt.Errorf("cannot auto-fix %s: not in a git repo: %w", filename, err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("cannot auto-fix %s: failed to get worktree: %w", filename, err)
	}

	status, err := wt.Status()
	if err != nil {
		return fmt.Errorf("cannot auto-fix %s: failed to get status: %w", filename, err)
	}

	repoRoot := wt.Filesystem.Root()
	relPath, err := filepath.Rel(repoRoot, filename)
	if err != nil {
		return fmt.Errorf("cannot auto-fix %s: failed to get relative path: %w", filename, err)
	}

	if fileStatus, ok := status[relPath]; ok {
		if fileStatus.Staging != git.Unmodified || fileStatus.Worktree != git.Unmodified {
			return fmt.Errorf("cannot auto-fix: %s has uncommitted changes\ncommit or stash changes first", filename)
		}
	}

	return nil
}
