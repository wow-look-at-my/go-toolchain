package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
)

// branchMarkerPrefix is the trailing require-line comment that pins a
// dependency to a branch instead of the module's default branch:
//
//	require github.com/wow-look-at-my/foo v0.0.0-... // go-toolchain:branch=v1
const branchMarkerPrefix = "go-toolchain:branch="

// trackedBranch returns the branch a require line is pinned to follow, or ""
// if it carries no branchMarkerPrefix comment. Matched by substring, not
// prefix, so it still finds the marker on a line combined with an
// "// indirect; ..." comment (see setIndirect in x/mod/modfile).
func trackedBranch(req *modfile.Require) string {
	for _, c := range req.Syntax.Suffix {
		idx := strings.Index(c.Token, branchMarkerPrefix)
		if idx == -1 {
			continue
		}
		return strings.TrimSpace(c.Token[idx+len(branchMarkerPrefix):])
	}
	return ""
}

// UpdateTrackedBranchDeps re-resolves every require carrying a
// go-toolchain:branch comment to that branch's current HEAD, rewriting its
// pseudo-version in place. go.mod still always records one concrete,
// go.sum-verified pseudo-version -- reproducibility is untouched -- this only
// keeps that version pointed at the chosen branch instead of drifting back to
// the module's default branch the way the org-deps auto-updater otherwise
// would (checkDepLive in deps.go resolves against the proxy's @latest, which
// is the default branch by construction; listDirectDeps excludes tracked
// requires from that path so the two never fight over the same line).
//
// Returns whether go.mod changed, so the caller knows to re-run `go mod tidy`.
func UpdateTrackedBranchDeps(r runner.CommandRunner) (bool, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return false, nil // Let go mod tidy handle a missing go.mod
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return false, nil // Let go mod tidy handle parse errors
	}

	changed := false
	for _, req := range f.Require {
		branch := trackedBranch(req)
		if branch == "" {
			continue
		}
		if req.Indirect {
			logger.Warn("%s tracks branch %q but is marked indirect; make it a direct dependency or drop the go-toolchain:branch comment", req.Mod.Path, branch)
			continue
		}

		version, err := resolveVersionViaGit(r, req.Mod.Path, "refs/heads/"+branch)
		if err != nil {
			return changed, fmt.Errorf("failed to resolve %s@%s: %w", req.Mod.Path, branch, err)
		}
		if version == req.Mod.Version {
			continue
		}

		if !jsonOutput {
			logger.Info("⇒ Updating %s (tracking %s): %s -> %s", req.Mod.Path, branch, req.Mod.Version, version)
		}
		if err := f.AddRequire(req.Mod.Path, version); err != nil {
			return changed, fmt.Errorf("failed to update %s: %w", req.Mod.Path, err)
		}
		changed = true
	}

	if !changed {
		return false, nil
	}

	newData, err := f.Format()
	if err != nil {
		return false, fmt.Errorf("failed to format go.mod: %w", err)
	}
	if err := os.WriteFile("go.mod", newData, 0644); err != nil {
		return false, fmt.Errorf("failed to write go.mod: %w", err)
	}

	return true, nil
}
