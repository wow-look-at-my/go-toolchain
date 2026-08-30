package vet

import (
	"fmt"
	"path/filepath"

	git "github.com/go-git/go-git/v5"
)

// resolveLinks spells path as the kernel resolves it, so darwin's /var and
// /private/var compare equal.
func resolveLinks(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// checkFileCommittedGoGit checks file status using the go-git library.
// go-git v5 cannot read an index written under index.skipHash/feature.manyFiles
// (a recent git writes an empty trailer hash): Status fails with "invalid
// checksum" — the upstream fix (https://github.com/go-git/go-git/pull/2181) is
// main-only, unreleased. The
// git-CLI fallback in checkFileCommittedByName covers such repos
// (regression-tested by TestCheckFileCommittedByName_ManyFilesIndex).
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

	// go-git answers a resolved path and filename carries the caller's spelling.
	relPath, err := filepath.Rel(resolveLinks(wt.Filesystem.Root()), resolveLinks(filename))
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
