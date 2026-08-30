package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
)

// OrgModulePrefixes are module-path prefixes whose deps must be branch-tracked, not version-pinned.
var OrgModulePrefixes = []string{"github.com/wow-look-at-my/"}

// pinnedMarker opts a require/replace line out of branch tracking, keeping its version pin; put the reason after it.
const pinnedMarker = "go-toolchain:pinned"

// isOrgModule reports whether a module path belongs to an org whose
// dependencies must be branch-tracked.
func isOrgModule(path string) bool {
	for _, prefix := range OrgModulePrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// hasPinnedMarker reports whether a line carries the deliberate-version-pin opt-out, matched by
// substring since the marker can share a comment with "// indirect".
func hasPinnedMarker(line *modfile.Line) bool {
	if line == nil {
		return false
	}
	for _, c := range line.Suffix {
		if strings.Contains(c.Token, pinnedMarker) {
			return true
		}
	}
	return false
}

// EnforceOrgBranchTracking makes the branch pin the canonical form for every
// org-owned dependency: an org require or replace that carries a plain version
// and no marker gets the bare auto-branch comment added.
// UpdateTrackedBranchDeps then owns the line from that point on and re-resolves
// it every run, so the version stops being a snapshot of whenever someone last
// ran `go get`.
//
// The rewrite is the enforcement: locally the developer sees the diff and
// commits it, and in CI the resulting dirty tree fails the build
// (checkDirtyInCI), the same contract as every other go.mod mutation in this
// pipeline. Runs before UpdateTrackedBranchDeps so a line marked here is
// resolved to its branch HEAD in the same run.
//
// Indirect requires are out of scope. Branch tracking skips them (a per-line
// branch pin on a transitively resolved dependency does not mean what it
// looks like -- see UpdateTrackedBranchDeps), so demanding a marker there
// would demand a marker that does nothing. A require whose version a replace
// overrides is out of scope too: the replacement supplies the version that
// reaches the build, and the replace line is what gets marked instead. Only a
// replace that NAMES A VERSION does that -- see versionReplaced.
//
// Returns whether go.mod changed.
func EnforceOrgBranchTracking(r runner.CommandRunner) (bool, error) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return false, nil // Let go mod tidy handle a missing go.mod
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return false, nil // Let go mod tidy handle parse errors
	}

	replaced := versionReplaced(f)

	changed := false
	for _, req := range f.Require {
		if req.Indirect || replaced.Contains(req.Mod.Path) || !isOrgModule(req.Mod.Path) {
			continue
		}
		if hasPinnedMarker(req.Syntax) {
			continue
		}
		changed = markBranchTracked(r, req.Syntax, req.Mod.Path, req.Mod.Version) || changed
	}

	for _, rep := range f.Replace {
		if isLocalReplacement(rep.New) || !isOrgModule(rep.New.Path) {
			continue
		}
		if hasPinnedMarker(rep.Syntax) {
			continue
		}
		changed = markBranchTracked(r, rep.Syntax, rep.New.Path, rep.New.Version) || changed
	}

	// The indirect lines are read last. The loops above decide which
	// repositories this run tracks, and the warning has to know that.
	for _, req := range f.Require {
		if req.Indirect {
			warnIndirectOrgRequire(f, req, replaced.Contains(req.Mod.Path))
		}
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

// versionReplaced returns modules whose require defers to a version-naming replace; a
// local-directory replace does not count, since only this module ever sees it.
func versionReplaced(f *modfile.File) set.Set[string] {
	out := set.New[string]()
	for _, rep := range f.Replace {
		if !isLocalReplacement(rep.New) {
			out.Add(rep.Old.Path)
		}
	}
	return out
}

// trackedSiblingOf reports whether mod ships from a repository that a tracked
// direct require already follows. The sibling resolution owns the line from
// there: it requires every module of that repository at the same commit and
// marks each of them (siblingRequires).
func trackedSiblingOf(f *modfile.File, mod string) bool {
	owner, repo, ok := gitHubOwnerRepo(mod)
	if !ok {
		return false
	}
	for _, req := range f.Require {
		if req.Indirect || !isTracked(req.Syntax) {
			continue
		}
		o, r, ok := gitHubOwnerRepo(req.Mod.Path)
		if ok && o == owner && r == repo {
			return true
		}
	}
	return false
}

// warnIndirectOrgRequire flags an org dependency that is version-pinned on an indirect require,
// where the branch marker cannot go: branch tracking skips indirect lines, so a marker there
// would track nothing. Saying nothing here would read as compliance, so it warns instead.
// The repair is a human call: promote the module to a direct require (what every consumer sees),
// or pin the effective version with a tracked replace (main-module-only, moves only this
// module's own build). A sibling of an already-tracked line is exempt: this run moves that
// line itself, later in the same phase.
func warnIndirectOrgRequire(f *modfile.File, req *modfile.Require, coveredByReplace bool) {
	if coveredByReplace || !isOrgModule(req.Mod.Path) {
		return
	}
	if isTracked(req.Syntax) || hasPinnedMarker(req.Syntax) {
		return
	}
	if trackedSiblingOf(f, req.Mod.Path) {
		return
	}
	logger.Warn("%s is version-pinned at %s and indirect, so it cannot carry a branch marker. Promote it to a direct require, which is the only repair a consumer of this module sees. To move just this module's own builds, pin the effective version with `replace %s => %s <version> // %s` -- a replace is main-module-only, so it covers this module's indirect requires and nobody else's. Deliberate? Say so with a trailing // %s <reason> comment.",
		req.Mod.Path, req.Mod.Version, req.Mod.Path, req.Mod.Path, autoBranchMarker, pinnedMarker)
}

// markBranchTracked brings a go.mod line to the canonical marker and reports whether it changed.
// An unmarked line gets the bare auto-branch comment, which costs no lookup: it names no branch,
// so there is nothing to ask until the line is resolved.
//
// A line still carrying the legacy branch=<name> spelling is migrated, dropping a hardcoded name
// that merely repeats the default branch: `branch=v1` becomes `auto-branch=v1`, while
// `branch=master` on a repo whose default IS master becomes plain `auto-branch`.
//
// Telling those apart is the only lookup here; a remote that cannot answer keeps the name, so the
// migration is never a change of meaning. It warns, since a kept name may be worth dropping later.
func markBranchTracked(r runner.CommandRunner, line *modfile.Line, mod, version string) bool {
	m := parseMarker(line)
	if m.tracks && !m.legacy {
		return false
	}

	marked := marker{tracks: true, branch: m.branch}
	if m.legacy && m.branch != "" {
		def, err := defaultBranchOf(r, mod)
		switch {
		case err != nil:
			logger.Warn("%s follows branch %s, and whether that is its default branch could not be resolved (%v), so the migrated marker keeps the name: drop the =%s to follow whatever the default is called", mod, m.branch, err, m.branch)
		case def == m.branch:
			marked.branch = ""
		}
	}
	if !jsonOutput {
		switch {
		case !m.tracks:
			logger.Info("⇒ %s: version pin %s is not branch-tracked, marking it to follow %s", mod, version, marked.meaning())
		case m.legacy:
			logger.Info("⇒ %s: %s%s becomes %s", mod, legacyBranchMarker, m.branch, marked.comment())
		}
	}
	setMarker(line, marked)
	return true
}

// setMarker replaces any go-toolchain tracking comment on a line with m's.
// A line with an existing suffix comment gets the marker JOINED to it, "// indirect;
// go-toolchain:...", the same way x/mod's setIndirect does. A further Suffix comment would
// render on its own line below, which corrupts the block.
func setMarker(line *modfile.Line, m marker) {
	kept := line.Suffix[:0]
	for _, c := range line.Suffix {
		token := stripMarkers(c.Token)
		if token == "" {
			continue
		}
		c.Token = token
		kept = append(kept, c)
	}
	line.Suffix = kept
	if len(line.Suffix) == 0 {
		line.Suffix = []modfile.Comment{{Token: "// " + m.comment(), Suffix: true}}
		return
	}
	line.Suffix[0].Token += "; " + m.comment()
}

// stripMarkers removes any go-toolchain tracking marker from a comment token,
// returning "" when nothing but the marker was there.
func stripMarkers(token string) string {
	for _, mark := range []string{autoBranchMarker, legacyBranchMarker} {
		i := strings.Index(token, mark)
		if i == -1 {
			continue
		}
		rest := token[i+len(mark):]
		if _, after, found := strings.Cut(rest, " "); found {
			token = token[:i] + after // a trailing note after the marker stays
		} else {
			token = token[:i]
		}
	}
	token = strings.TrimRight(strings.TrimSpace(token), ";")
	if token == "//" {
		return ""
	}
	return strings.TrimSpace(token)
}

// untrackedOrgDeps returns the org-owned dependencies that carry a version
// pin with neither a branch marker nor the deliberate-pin opt-out. It reads
// only go.mod, so the up-to-date fast exit can consult it without a network
// call: a tree that has not changed since the last green run can still
// predate this requirement, and the fast exit would otherwise skip the run
// that adds the markers.
func untrackedOrgDeps() []string {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return nil
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil
	}

	replaced := versionReplaced(f)

	var out []string
	for _, req := range f.Require {
		if req.Indirect || replaced.Contains(req.Mod.Path) || !isOrgModule(req.Mod.Path) {
			continue
		}
		if !isTracked(req.Syntax) && !hasPinnedMarker(req.Syntax) {
			out = append(out, req.Mod.Path)
		}
	}
	for _, rep := range f.Replace {
		if isLocalReplacement(rep.New) || !isOrgModule(rep.New.Path) {
			continue
		}
		if !isTracked(rep.Syntax) && !hasPinnedMarker(rep.Syntax) {
			out = append(out, rep.New.Path)
		}
	}
	return out
}
