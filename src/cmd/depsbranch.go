package cmd

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/module"
)

// branchMarkerPrefix is the trailing comment that pins a dependency to a
// branch instead of the module's default branch. It rides either the require
// line or, for a fork consumed through a replacement, the replace line:
//
//	require github.com/wow-look-at-my/foo v0.0.0-... // go-toolchain:branch=v1
//	replace upstream.example/foo => github.com/wow-look-at-my/foo v0.0.0-... // go-toolchain:branch=master
const branchMarkerPrefix = "go-toolchain:branch="

// trackedBranch returns the branch a go.mod line is pinned to follow, or ""
// if it carries no branchMarkerPrefix comment. Matched by substring, not
// prefix, so it still finds the marker on a line combined with an
// "// indirect; ..." comment (see setIndirect in x/mod/modfile).
func trackedBranch(line *modfile.Line) string {
	if line == nil {
		return ""
	}
	for _, c := range line.Suffix {
		idx := strings.Index(c.Token, branchMarkerPrefix)
		if idx == -1 {
			continue
		}
		return strings.TrimSpace(c.Token[idx+len(branchMarkerPrefix):])
	}
	return ""
}

// isLocalReplacement reports whether a replacement points at a directory on
// this filesystem rather than a module version: those carry no version and
// have no remote to resolve a branch against.
func isLocalReplacement(target module.Version) bool {
	return target.Version == "" ||
		strings.HasPrefix(target.Path, "./") ||
		strings.HasPrefix(target.Path, "../") ||
		filepath.IsAbs(target.Path)
}

// branchPin is a resolved version and the branch whose head it came from.
type branchPin struct {
	version string
	branch  string
}

// UpdateTrackedBranchDeps re-resolves every require and replace carrying a
// go-toolchain:branch comment to that branch's current HEAD, rewriting its
// pseudo-version in place. go.mod still always records one concrete,
// go.sum-verified pseudo-version -- reproducibility is untouched -- this only
// keeps that version pointed at the chosen branch instead of drifting back to
// the module's default branch the way the org-deps auto-updater otherwise
// would (checkDepLive in deps.go resolves against the proxy's @latest, which
// is the default branch by construction; listDirectDeps excludes tracked
// lines -- and requires covered by a tracked replace -- from that path so the
// two never fight over the same dependency).
//
// A tracked module that shares its repository with other modules brings them
// along at the same commit (siblingRequires), because a multi-module repo
// cannot pin itself: the sibling require inside it necessarily names an
// earlier commit than the one being published. Requiring them here is what
// makes a tracked pin mean the whole repository at one commit, rather than one
// module at one commit and its siblings at whatever came before it.
//
// The rewritten version is a CACHE of the last resolution, not a contract:
// the marker says "follow this branch", and every run re-answers it. That is
// why the CI dirty check excludes this rewrite (checkDirtyInCI) -- a commit
// whose whole content is a hash nobody chose is noise, and demanding one would
// make the marker mean a bump commit per upstream push, which is the opposite
// of what it is for.
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

	mainModule := ""
	if f.Module != nil {
		mainModule = f.Module.Mod.Path
	}

	// Resolve everything first, write once. Which lines to touch is not known
	// until every tracked line has been answered, since a tracked module can
	// name siblings -- and a failure partway through then leaves go.mod
	// untouched rather than half-moved.
	resolved := map[string]branchPin{}
	siblings := map[string]branchPin{}
	for _, req := range f.Require {
		branch := trackedBranch(req.Syntax)
		if branch == "" || req.Indirect {
			continue
		}
		c, cleanup, err := fetchCommit(r, req.Mod.Path, "refs/heads/"+branch)
		if err != nil {
			return false, fmt.Errorf("failed to resolve %s@%s: %w", req.Mod.Path, branch, err)
		}
		resolved[req.Mod.Path] = branchPin{pseudoVersionFor(req.Mod.Path, c.Time, c.ShortHash), branch}
		sibs, sibErr := siblingRequires(r, c, mainModule)
		cleanup()
		if sibErr != nil {
			return false, fmt.Errorf("failed to resolve the modules %s shares a repository with at %s: %w", req.Mod.Path, branch, sibErr)
		}
		for mod, version := range sibs {
			siblings[mod] = branchPin{version, branch}
		}
	}

	for _, req := range f.Require {
		branch := trackedBranch(req.Syntax)
		if branch == "" || !req.Indirect {
			continue
		}
		if _, managed := siblings[req.Mod.Path]; managed {
			continue // this run owns the line; tidy is what marked it indirect
		}
		logger.Warn("%s tracks branch %q but is marked indirect; make it a direct dependency, track it through a replace instead (replace %s => <repo> <version> // go-toolchain:branch=%s -- a replace is main-module-only, so it covers direct and indirect requires alike), or drop the go-toolchain:branch comment",
			req.Mod.Path, branch, req.Mod.Path, branch)
	}

	changed := false
	for _, req := range f.Require {
		pin, ok := resolved[req.Mod.Path]
		if !ok || pin.version == req.Mod.Version {
			continue
		}
		if !jsonOutput {
			logger.Info("⇒ Updating %s (tracking %s): %s -> %s", req.Mod.Path, pin.branch, req.Mod.Version, pin.version)
		}
		if err := f.AddRequire(req.Mod.Path, pin.version); err != nil {
			return false, fmt.Errorf("failed to update %s: %w", req.Mod.Path, err)
		}
		changed = true
	}

	for _, mod := range slices.Sorted(maps.Keys(siblings)) {
		if _, direct := resolved[mod]; direct {
			continue // its own tracked line already resolved it
		}
		moved, err := requireSiblingAt(f, mod, siblings[mod])
		if err != nil {
			return false, err
		}
		changed = changed || moved
	}

	// The replacement's own path and version are what get resolved: a fork
	// keeps upstream's module path, so the require line names upstream and
	// tracking its branch is never what the marker means.
	for _, rep := range f.Replace {
		branch := trackedBranch(rep.Syntax)
		if branch == "" {
			continue
		}
		if isLocalReplacement(rep.New) {
			logger.Warn("%s is replaced by the local directory %s, which has no branch to track; drop the go-toolchain:branch comment", rep.Old.Path, rep.New.Path)
			continue
		}

		version, err := resolveVersionViaGit(r, rep.New.Path, "refs/heads/"+branch)
		if err != nil {
			return changed, fmt.Errorf("failed to resolve %s@%s: %w", rep.New.Path, branch, err)
		}
		if version == rep.New.Version {
			continue
		}

		if !jsonOutput {
			logger.Info("⇒ Updating %s (tracking %s): %s -> %s", rep.New.Path, branch, rep.New.Version, version)
		}
		if err := f.AddReplace(rep.Old.Path, rep.Old.Version, rep.New.Path, version); err != nil {
			return changed, fmt.Errorf("failed to update %s: %w", rep.New.Path, err)
		}
		changed = true
	}

	if !changed {
		return false, nil
	}

	f.Cleanup()
	newData, err := f.Format()
	if err != nil {
		return false, fmt.Errorf("failed to format go.mod: %w", err)
	}
	if err := os.WriteFile("go.mod", newData, 0644); err != nil {
		return false, fmt.Errorf("failed to write go.mod: %w", err)
	}

	return true, nil
}

// requireSiblingAt puts mod in go.mod at the commit its repository resolved
// to, adding the require if it is absent and marking it tracked so later runs
// keep moving it. It reports whether go.mod changed.
//
// A deliberate version pin wins: hasPinnedMarker is how someone says they want
// one specific version of this module, and moving with its siblings is exactly
// what that opts out of.
func requireSiblingAt(f *modfile.File, mod string, pin branchPin) (bool, error) {
	existing := findRequire(f, mod)
	if existing != nil && hasPinnedMarker(existing.Syntax) {
		return false, nil
	}
	if existing != nil && existing.Mod.Version == pin.version && trackedBranch(existing.Syntax) == pin.branch {
		return false, nil
	}

	if !jsonOutput {
		if existing == nil {
			logger.Info("⇒ Requiring %s at %s: it ships from the same commit as a module tracking %s", mod, pin.version, pin.branch)
		} else if existing.Mod.Version != pin.version {
			logger.Info("⇒ Updating %s (same repository, tracking %s): %s -> %s", mod, pin.branch, existing.Mod.Version, pin.version)
		}
	}
	if err := f.AddRequire(mod, pin.version); err != nil {
		return false, fmt.Errorf("failed to require %s: %w", mod, err)
	}
	if added := findRequire(f, mod); added != nil && trackedBranch(added.Syntax) == "" {
		appendBranchMarker(added.Syntax, pin.branch)
	}
	return true, nil
}

// findRequire returns the require line for a module path, or nil.
func findRequire(f *modfile.File, mod string) *modfile.Require {
	for _, req := range f.Require {
		if req.Mod.Path == mod {
			return req
		}
	}
	return nil
}

// trackedBranchDepsMoved reports whether any branch-tracking require or
// replace now resolves to a different commit than go.mod records. It is what
// lets the up-to-date fast exit (uptodate.go) see the one input that is not a
// file.
//
// A repository with no tracked line pays nothing: the loops make no call at
// all. One that has them pays a ref resolution per tracked module, which is
// the cost of the guarantee that opting into branch tracking bought.
//
// It answers FALSE when it cannot tell -- an unreadable or unparseable go.mod,
// or a resolution that failed. Those are conditions for the real run to
// report, and reporting them from inside a cache check would turn an
// unreachable remote into a full rebuild rather than a network error.
func trackedBranchDepsMoved(r runner.CommandRunner) bool {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return false
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return false
	}
	for _, req := range f.Require {
		branch := trackedBranch(req.Syntax)
		if branch == "" || req.Indirect {
			continue
		}
		version, err := resolveVersionViaGit(r, req.Mod.Path, "refs/heads/"+branch)
		if err != nil {
			continue
		}
		if version != req.Mod.Version {
			return true
		}
	}
	for _, rep := range f.Replace {
		branch := trackedBranch(rep.Syntax)
		if branch == "" || isLocalReplacement(rep.New) {
			continue
		}
		version, err := resolveVersionViaGit(r, rep.New.Path, "refs/heads/"+branch)
		if err != nil {
			continue
		}
		if version != rep.New.Version {
			return true
		}
	}
	return false
}
