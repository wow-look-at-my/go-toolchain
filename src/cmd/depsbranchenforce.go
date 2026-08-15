package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
)

// orgModulePrefixes are the module-path prefixes whose dependencies must be
// branch-tracked rather than version-pinned. These are the org's own modules:
// they are co-developed with their consumers and carry no release cadence, so
// a version pin on one is a snapshot nobody updates, while the branch pin
// stays a declarative "follow this branch" that every run re-resolves.
var orgModulePrefixes = []string{"github.com/wow-look-at-my/"}

// pinnedMarker opts a line out of the branch-tracking requirement, keeping
// its version pin. Anything after the marker is free text, so the reason
// rides the line it justifies:
//
//	require github.com/wow-look-at-my/foo v1.2.3 // go-toolchain:pinned v2 API break
const pinnedMarker = "go-toolchain:pinned"

// isOrgModule reports whether a module path belongs to an org whose
// dependencies must be branch-tracked.
func isOrgModule(path string) bool {
	for _, prefix := range orgModulePrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

// hasPinnedMarker reports whether a line carries the deliberate-version-pin
// opt-out. Matched by substring for the same reason trackedBranch is: the
// marker can share the comment with "// indirect".
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
// org-owned dependency: an org require or replace that carries a plain
// version instead of a go-toolchain:branch comment gets the comment added,
// naming the module's own default branch. UpdateTrackedBranchDeps then owns
// the line from that point on and re-resolves it every run, so the version
// stops being a snapshot of whenever someone last ran `go get`.
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
// would demand one that does nothing. A require whose version is overridden
// by a replace is out of scope too: the replacement supplies the version that
// reaches the build, and the replace line is what gets marked instead.
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

	replaced := set.New[string]()
	for _, rep := range f.Replace {
		replaced.Add(rep.Old.Path)
	}

	changed := false
	for _, req := range f.Require {
		if req.Indirect {
			warnIndirectOrgRequire(req, replaced.Contains(req.Mod.Path))
			continue
		}
		if replaced.Contains(req.Mod.Path) || !isOrgModule(req.Mod.Path) {
			continue
		}
		if trackedBranch(req.Syntax) != "" || hasPinnedMarker(req.Syntax) {
			continue
		}
		if err := markBranchTracked(r, req.Syntax, req.Mod.Path, req.Mod.Version); err != nil {
			return changed, err
		}
		changed = true
	}

	for _, rep := range f.Replace {
		if isLocalReplacement(rep.New) || !isOrgModule(rep.New.Path) {
			continue
		}
		if trackedBranch(rep.Syntax) != "" || hasPinnedMarker(rep.Syntax) {
			continue
		}
		if err := markBranchTracked(r, rep.Syntax, rep.New.Path, rep.New.Version); err != nil {
			return changed, err
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

// warnIndirectOrgRequire reports an org dependency that is version-pinned on
// an indirect require, where the marker this file adds everywhere else cannot
// go: branch tracking skips indirect lines, so writing one there would leave a
// comment that reads like a pin and tracks nothing.
//
// The repair a human has to choose between -- promote the module to a direct
// dependency, or pin the effective version with a tracked replace -- changes
// what the build resolves, so it is not one to make on someone's behalf.
// Saying nothing is the option this rules out: the module is version-pinned
// exactly like the ones being rewritten, and a silent skip is indistinguishable
// from compliance.
func warnIndirectOrgRequire(req *modfile.Require, coveredByReplace bool) {
	if coveredByReplace || !isOrgModule(req.Mod.Path) {
		return
	}
	if trackedBranch(req.Syntax) != "" || hasPinnedMarker(req.Syntax) {
		return
	}
	logger.Warn("%s is version-pinned at %s and indirect, so it cannot carry a branch marker: promote it to a direct require, or pin the version that reaches the build with `replace %s => %s <version> // %s<branch>` (main-module-only, so it covers indirect requires too). Deliberate? Say so with a trailing // %s <reason> comment.",
		req.Mod.Path, req.Mod.Version, req.Mod.Path, req.Mod.Path, branchMarkerPrefix, pinnedMarker)
}

// markBranchTracked appends the branch marker for mod's default branch to a
// go.mod line. Resolution failure is fatal rather than a warning: silently
// leaving the version pin in place would report a green run for a go.mod that
// does not meet the requirement.
func markBranchTracked(r runner.CommandRunner, line *modfile.Line, mod, version string) error {
	branch, err := resolveDefaultBranch(r, mod)
	if err != nil {
		return fmt.Errorf("%s is version-pinned to %s and must track a branch, but its default branch could not be resolved: %w (pin it deliberately with a trailing // %s <reason> comment if that is what you mean)", mod, version, err, pinnedMarker)
	}
	if !jsonOutput {
		logger.Info("⇒ %s: version pin %s is not branch-tracked, marking it to follow %s", mod, version, branch)
	}
	line.Suffix = append(line.Suffix, modfile.Comment{
		Token:  "// " + branchMarkerPrefix + branch,
		Suffix: true,
	})
	return nil
}

// resolveDefaultBranch returns the branch name a module's repository HEAD
// points at. `git ls-remote --symref <url> HEAD` reports it without cloning:
//
//	ref: refs/heads/master	HEAD
//	<hash>	HEAD
func resolveDefaultBranch(r runner.CommandRunner, mod string) (string, error) {
	gitURL, _, err := resolveGitURLAndRef(r, mod, "HEAD")
	if err != nil {
		return "", err
	}

	proc, err := runner.Cmd("git", "ls-remote", "--symref", gitURL, "HEAD").WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git ls-remote --symref %s failed: %w", gitURL, err)
	}
	out, _ := io.ReadAll(proc.Stdout())
	if err := proc.Wait(); err != nil {
		return "", fmt.Errorf("git ls-remote --symref %s failed: %w", gitURL, err)
	}

	for _, ln := range strings.Split(string(out), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(ln), "ref: ")
		if !ok {
			continue
		}
		ref := strings.Fields(rest)
		if len(ref) == 0 {
			continue
		}
		if branch, ok := strings.CutPrefix(ref[0], "refs/heads/"); ok && branch != "" {
			return branch, nil
		}
	}
	return "", fmt.Errorf("%s reported no symbolic HEAD", gitURL)
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

	replaced := set.New[string]()
	for _, rep := range f.Replace {
		replaced.Add(rep.Old.Path)
	}

	var out []string
	for _, req := range f.Require {
		if req.Indirect || replaced.Contains(req.Mod.Path) || !isOrgModule(req.Mod.Path) {
			continue
		}
		if trackedBranch(req.Syntax) == "" && !hasPinnedMarker(req.Syntax) {
			out = append(out, req.Mod.Path)
		}
	}
	for _, rep := range f.Replace {
		if isLocalReplacement(rep.New) || !isOrgModule(rep.New.Path) {
			continue
		}
		if trackedBranch(rep.Syntax) == "" && !hasPinnedMarker(rep.Syntax) {
			out = append(out, rep.New.Path)
		}
	}
	return out
}
