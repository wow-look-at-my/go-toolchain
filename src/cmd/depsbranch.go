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

// isLocalReplacement reports whether a replacement points at a directory on
// this filesystem rather than a module version: those carry no version and
// have no remote to resolve a branch against.
func isLocalReplacement(target module.Version) bool {
	return target.Version == "" ||
		strings.HasPrefix(target.Path, "./") ||
		strings.HasPrefix(target.Path, "../") ||
		filepath.IsAbs(target.Path)
}

// branchPin is a resolved version and the marker that answered for it.
type branchPin struct {
	version string
	marker  marker
}

// commitAnchor is the commit a require's siblings have to match, and how to
// get it: a ref to resolve for a line that follows a branch, or the commit a
// deliberate version pin already names.
type commitAnchor struct {
	ref  string
	hash string
	desc string
}

func (a commitAnchor) describe() string { return a.desc }

func (a commitAnchor) fetch(r runner.CommandRunner, mod string) (*gitCommit, func(), error) {
	if a.hash != "" {
		return fetchCommitAt(r, mod, a.hash)
	}
	return fetchCommit(r, mod, a.ref)
}

// siblingAnchor returns the commit a direct require's same-repository siblings
// must match, and whether it has one at all.
//
// A tracked line's anchor is its branch. A DELIBERATELY PINNED line has one
// too, and it is not the branch: cohesion is about the modules of one repo
// shipping together, so a line held at an old version holds its siblings at
// that same old commit. Following the branch there would pair a pinned module
// with siblings from today, which is the mismatch the pin exists to avoid.
func siblingAnchor(req *modfile.Require, m marker) (commitAnchor, bool) {
	if req.Indirect {
		return commitAnchor{}, false // an indirect line is somebody else's answer
	}
	if m.tracks && m.sibling == "" {
		return commitAnchor{ref: m.ref(), desc: m.describe()}, true
	}
	if !hasPinnedMarker(req.Syntax) || !isOrgModule(req.Mod.Path) {
		return commitAnchor{}, false
	}
	rev, err := module.PseudoVersionRev(req.Mod.Version)
	if err != nil || rev == "" {
		return commitAnchor{}, false // a tagged release names no commit to match
	}
	return commitAnchor{hash: rev, desc: "the commit pinned by " + req.Mod.Version}, true
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
	var temporary []temporaryBranch
	var unchecked []string
	for _, req := range f.Require {
		m := parseMarker(req.Syntax)
		anchor, isAnchor := siblingAnchor(req, m)
		if !isAnchor {
			continue
		}
		c, cleanup, err := anchor.fetch(r, req.Mod.Path)
		if err != nil {
			return false, fmt.Errorf("failed to resolve %s at %s: %w", req.Mod.Path, anchor.describe(), err)
		}
		if m.tracks {
			resolved[req.Mod.Path] = branchPin{pseudoVersionFor(req.Mod.Path, c.Time, c.ShortHash), m}
		}
		if m.branch != "" {
			t, isTemporary, checked := checkTemporaryBranch(req.Mod.Path, m.branch)
			switch {
			case isTemporary:
				t.module = req.Mod.Path
				temporary = append(temporary, t)
			case !checked:
				unchecked = append(unchecked, req.Mod.Path+"@"+m.branch)
			}
		}
		sibs, sibErr := siblingRequires(r, c, mainModule)
		cleanup()
		if sibErr != nil {
			return false, fmt.Errorf("failed to resolve the modules %s shares a repository with at %s: %w", req.Mod.Path, anchor.describe(), sibErr)
		}
		// The compatibility half names the branch the ANCHOR follows, which is
		// the closest an older release can come to "the commit that module
		// resolved to": it has no sibling marker, so the alternative is it
		// treating the line as unmarked and overwriting it.
		compat := m.branch
		if compat == "" {
			compat = c.DefaultBranch
		}
		for mod, version := range sibs {
			siblings[mod] = branchPin{version, marker{tracks: true, sibling: req.Mod.Path, compat: compat}}
		}
	}

	reportUncheckedBranches(unchecked)
	if err := reportTemporaryBranches(temporary); err != nil {
		return false, err
	}

	for _, req := range f.Require {
		m := parseMarker(req.Syntax)
		if !m.tracks || !req.Indirect {
			continue
		}
		if _, managed := siblings[req.Mod.Path]; managed {
			continue // this run owns the line; tidy is what marked it indirect
		}
		if m.sibling != "" {
			logger.Warn("%s matches the commit of %s, which no longer brings it in; drop the %s comment, or restore the require that used to need it",
				req.Mod.Path, m.sibling, siblingMarker)
			continue
		}
		logger.Warn("%s follows %s but is marked indirect; make it a direct dependency, track it through a replace instead (replace %s => <repo> <version> // %s -- a replace is main-module-only, so it covers direct and indirect requires alike), or drop the %s comment",
			req.Mod.Path, m.describe(), req.Mod.Path, m.comment(), autoBranchMarker)
	}

	changed := false
	for _, req := range f.Require {
		pin, ok := resolved[req.Mod.Path]
		if !ok || pin.version == req.Mod.Version {
			continue
		}
		if !jsonOutput {
			logger.Info("⇒ Updating %s (following %s): %s -> %s", req.Mod.Path, pin.marker.describe(), req.Mod.Version, pin.version)
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
		m := parseMarker(rep.Syntax)
		if !m.tracks {
			continue
		}
		if isLocalReplacement(rep.New) {
			logger.Warn("%s is replaced by the local directory %s, which has no branch to track; drop the %s comment", rep.Old.Path, rep.New.Path, autoBranchMarker)
			continue
		}

		if m.branch != "" {
			t, isTemporary, checked := checkTemporaryBranch(rep.New.Path, m.branch)
			switch {
			case isTemporary:
				t.module = rep.New.Path
				if err := reportTemporaryBranches([]temporaryBranch{t}); err != nil {
					return changed, err
				}
			case !checked:
				reportUncheckedBranches([]string{rep.New.Path + "@" + m.branch})
			}
		}

		version, err := resolveVersionViaGit(r, rep.New.Path, m.ref())
		if err != nil {
			return changed, fmt.Errorf("failed to resolve %s at %s: %w", rep.New.Path, m.describe(), err)
		}
		if version == rep.New.Version {
			continue
		}

		if !jsonOutput {
			logger.Info("⇒ Updating %s (following %s): %s -> %s", rep.New.Path, m.describe(), rep.New.Version, version)
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
	if existing != nil && existing.Mod.Version == pin.version && parseMarker(existing.Syntax) == pin.marker {
		return false, nil
	}

	if !jsonOutput {
		if existing == nil {
			logger.Info("⇒ Requiring %s at %s: it ships from the same commit as %s", mod, pin.version, pin.marker.sibling)
		} else if existing.Mod.Version != pin.version {
			logger.Info("⇒ Updating %s (same repository as %s): %s -> %s", mod, pin.marker.sibling, existing.Mod.Version, pin.version)
		}
	}
	if err := f.AddRequire(mod, pin.version); err != nil {
		return false, fmt.Errorf("failed to require %s: %w", mod, err)
	}
	if added := findRequire(f, mod); added != nil {
		setMarker(added.Syntax, pin.marker)
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
		m := parseMarker(req.Syntax)
		if !m.tracks || m.sibling != "" || req.Indirect {
			continue // a sibling moves with its anchor, which is checked here
		}
		version, err := resolveVersionViaGit(r, req.Mod.Path, m.ref())
		if err != nil {
			continue
		}
		if version != req.Mod.Version {
			return true
		}
	}
	for _, rep := range f.Replace {
		m := parseMarker(rep.Syntax)
		if !m.tracks || isLocalReplacement(rep.New) {
			continue
		}
		version, err := resolveVersionViaGit(r, rep.New.Path, m.ref())
		if err != nil {
			continue
		}
		if version != rep.New.Version {
			return true
		}
	}
	return false
}
