package cmd

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

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

// commitAnchor is the ref a require's same-repository siblings must match.
type commitAnchor struct {
	ref string
	// branch is the marker's branch, empty for default; it lets sibling lines of a repo follow different branches.
	branch string
	desc   string
}

func (a commitAnchor) describe() string { return a.desc }

func (a commitAnchor) fetch(r runner.CommandRunner, mod string) (*gitCommit, func(), error) {
	return fetchCommit(r, mod, a.ref)
}

// repoResolution is a repository's settled answer: the commit every module lands on, plus its reachable modules.
type repoResolution struct {
	anchor   commitAnchor
	commit   *gitCommit
	siblings map[string]string
}

// repoResolver answers each repository a SINGLE time, so a multi-module repo cannot land on divergent commits.
// It is safe for concurrent use.
type repoResolver struct {
	r        runner.CommandRunner
	main     string
	mu       sync.Mutex
	resolved []*repoResolution
	cleanups []func()
}

// find returns the settled resolution covering mod under anchor, or nil. rr.mu must be held.
func (rr *repoResolver) find(mod string, anchor commitAnchor) *repoResolution {
	for _, res := range rr.resolved {
		if res.anchor == anchor && inRepo(mod, res.commit.RepoRoot) {
			return res
		}
	}
	return nil
}

// at returns the resolution covering mod under anchor, fetching the repository
// as soon as any of its modules asks, and reusing that answer afterward.
// Concurrent callers for the same repository each fetch, and whichever settles
// sooner answers for all of them.
func (rr *repoResolver) at(mod string, anchor commitAnchor) (*repoResolution, error) {
	rr.mu.Lock()
	res := rr.find(mod, anchor)
	rr.mu.Unlock()
	if res != nil {
		return res, nil
	}

	c, cleanup, err := anchor.fetch(rr.r, mod)
	if err != nil {
		return nil, err
	}
	sibs, err := siblingRequires(rr.r, c, rr.main)
	if err != nil {
		cleanup()
		return nil, err
	}

	rr.mu.Lock()
	defer rr.mu.Unlock()
	if settled := rr.find(mod, anchor); settled != nil {
		cleanup()
		return settled, nil
	}
	rr.cleanups = append(rr.cleanups, cleanup)
	res = &repoResolution{anchor: anchor, commit: c, siblings: sibs}
	rr.resolved = append(rr.resolved, res)
	return res, nil
}

// close removes the temporary repositories, kept readable until then since a resolution is reused by later modules.
func (rr *repoResolver) close() {
	for _, cleanup := range rr.cleanups {
		cleanup()
	}
	rr.cleanups = nil
}

// siblingAnchor returns the commit a require's siblings must match, and
// whether it has an anchor at all: a tracked require, direct or indirect.
func siblingAnchor(req *modfile.Require, m marker, bm *branchMatcher) (commitAnchor, bool) {
	if !m.tracks {
		return commitAnchor{}, false
	}
	mod := req.Mod.Path
	return commitAnchor{ref: bm.ref(mod, m), branch: bm.branchFor(mod, m), desc: bm.describe(mod, m)}, true
}

// trackedLine is a tracked require's resolution and, for a named branch, what
// its pull-request check found.
type trackedLine struct {
	req    *modfile.Require
	m      marker
	anchor commitAnchor
	res    *repoResolution
	err    error

	temporary            temporaryBranch
	isTemporary, checked bool
}

// resolveTrackedLines resolves every tracked require concurrently, since each
// costs several network round trips, and returns them in go.mod order.
func resolveTrackedLines(reqs []*modfile.Require, resolver *repoResolver, bm *branchMatcher) []*trackedLine {
	var lines []*trackedLine
	var wg sync.WaitGroup
	for _, req := range reqs {
		m := parseMarker(req.Syntax)
		if !m.tracks {
			continue
		}
		l := &trackedLine{req: req, m: m}
		lines = append(lines, l)
		wg.Go(func() {
			l.anchor, _ = siblingAnchor(req, m, bm)
			l.res, l.err = resolver.at(req.Mod.Path, l.anchor)
			if l.err == nil && m.branch != "" {
				l.temporary, l.isTemporary, l.checked = checkTemporaryBranch(req.Mod.Path, m.branch)
			}
		})
	}
	wg.Wait()
	return lines
}

// UpdateTrackedBranchDeps re-resolves every require and replace carrying a
// go-toolchain:branch comment to that branch's current HEAD, rewriting its
// pseudo-version in place. go.mod still always records a concrete,
// go.sum-verified pseudo-version -- reproducibility is untouched -- this only
// keeps that version pointed at the chosen branch instead of drifting back to
// the module's default branch the way the org-deps auto-updater otherwise
// would (checkDepLive in deps.go resolves against the proxy's @latest, which
// is the default branch by construction; listDirectDeps excludes tracked
// lines -- and requires covered by a tracked replace -- from that path so the
// paths never fight over the same dependency).
//
// A tracked module that shares its repository with other modules brings them
// along at the same commit (siblingRequires), because a multi-module repo
// cannot pin itself: the sibling require inside it necessarily names an
// earlier commit than the commit being published. Requiring them here is what
// makes a tracked pin mean the whole repository at the same commit, rather than
// the tracked module alone with its siblings at whatever came before it.
//
// The rewritten version is a CACHE of the last resolution, not a contract:
// the marker says "follow this branch", and every run re-answers it. That is
// why the CI dirty check excludes this rewrite (checkDirtyInCI) -- a commit
// whose whole content is a hash nobody chose is noise, and demanding it would
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

	// Resolve everything up front, then write, so a failure partway through leaves go.mod untouched, not half-moved.
	resolver := &repoResolver{r: r, main: mainModule}
	defer resolver.close()
	bm := newBranchMatcher(r)

	resolved := map[string]branchPin{}
	siblings := map[string]branchPin{}
	var temporary []temporaryBranch
	var unchecked []string
	for _, l := range resolveTrackedLines(f.Require, resolver, bm) {
		mod := l.req.Mod.Path
		if l.err != nil {
			return false, fmt.Errorf("failed to resolve %s at %s: %w", mod, l.anchor.describe(), l.err)
		}
		resolved[mod] = branchPin{pseudoVersionFor(mod, l.res.commit.Time, l.res.commit.ShortHash), l.m}
		if l.m.branch != "" {
			switch {
			case l.isTemporary:
				l.temporary.module = mod
				temporary = append(temporary, l.temporary)
			case !l.checked:
				unchecked = append(unchecked, mod+"@"+l.m.branch)
			}
		}
		// A sibling carries the marker of the line that brought it in; cohesion is the resolver's doing.
		for sib, version := range l.res.siblings {
			siblings[sib] = branchPin{version, l.m}
		}
	}

	reportUncheckedBranches(unchecked)
	if err := reportTemporaryBranches(temporary); err != nil {
		return false, err
	}

	changed := false
	for _, req := range f.Require {
		pin, ok := resolved[req.Mod.Path]
		if !ok || pin.version == req.Mod.Version {
			continue
		}
		if !jsonOutput {
			logger.Info("⇒ Updating %s (following %s): %s -> %s", req.Mod.Path, bm.describe(req.Mod.Path, pin.marker), req.Mod.Version, pin.version)
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

		version, err := resolveVersionViaGit(r, rep.New.Path, bm.ref(rep.New.Path, m))
		if err != nil {
			return changed, fmt.Errorf("failed to resolve %s at %s: %w", rep.New.Path, bm.describe(rep.New.Path, m), err)
		}
		if version == rep.New.Version {
			continue
		}

		if !jsonOutput {
			logger.Info("⇒ Updating %s (following %s): %s -> %s", rep.New.Path, bm.describe(rep.New.Path, m), rep.New.Version, version)
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
func requireSiblingAt(f *modfile.File, mod string, pin branchPin) (bool, error) {
	existing := findRequire(f, mod)
	if existing != nil && existing.Mod.Version == pin.version && parseMarker(existing.Syntax) == pin.marker {
		return false, nil
	}

	if !jsonOutput {
		if existing == nil {
			logger.Info("⇒ Requiring %s at %s: it ships from the commit its repository resolved to", mod, pin.version)
		} else if existing.Mod.Version != pin.version {
			logger.Info("⇒ Updating %s (same repository, one commit): %s -> %s", mod, existing.Mod.Version, pin.version)
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
// lets the up-to-date fast exit (uptodate.go) see the sole input that is not a
// file.
//
// A repository with no tracked line pays nothing: the loops make no call at
// all. A repository that has them pays a ref resolution per tracked module, which is
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
	bm := newBranchMatcher(r)
	var pinned []module.Version
	var markers []marker
	for _, req := range f.Require {
		if m := parseMarker(req.Syntax); m.tracks {
			pinned, markers = append(pinned, req.Mod), append(markers, m)
		}
	}
	for _, rep := range f.Replace {
		if m := parseMarker(rep.Syntax); m.tracks && !isLocalReplacement(rep.New) {
			pinned, markers = append(pinned, rep.New), append(markers, m)
		}
	}

	var moved atomic.Bool
	var wg sync.WaitGroup
	for i, pin := range pinned {
		wg.Go(func() {
			version, err := resolveVersionViaGit(r, pin.Path, bm.ref(pin.Path, markers[i]))
			if err == nil && version != pin.Version {
				moved.Store(true)
			}
		})
	}
	wg.Wait()
	return moved.Load()
}
