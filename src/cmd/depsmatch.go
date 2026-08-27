// Two repositories developed in tandem carry the same branch name: the change
// spans both, and neither half is finished without the other. A bare
// auto-branch marker therefore follows the dependency's branch of THIS
// repository's name when the dependency has one, and its default branch
// otherwise.
//
// That is what makes the marker work while the change is in flight and again
// after it lands, with nothing written down. On the feature branch each side
// builds against the other's feature branch. The merge deletes the branch, the
// match stops matching, and both sides fall back to the default branch, which
// now carries the same code. Nothing has to be repointed, so nothing is left
// pointing at a branch that no longer exists.
//
// A name in the marker (auto-branch=<name>) is a deliberate, permanent choice
// and is never matched against: it says which branch, and the answer does not
// depend on where the reader is standing.
package cmd

import (
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// branchMatcher answers which ref a marker follows, asking each dependency
// repository once. A run builds one and passes it along, so the fast-exit
// check and the rewrite cannot disagree about what a line resolves to.
type branchMatcher struct {
	r runner.CommandRunner
	// branch is this repository's checked-out branch, empty when there is none to match.
	branch string
	// asked keeps the branch lookup lazy: a go.mod with no bare marker pays nothing.
	asked bool
	// seen maps a module path to the branch it matched, "" for the default branch.
	seen map[string]string
}

func newBranchMatcher(r runner.CommandRunner) *branchMatcher {
	return &branchMatcher{r: r, seen: map[string]string{}}
}

// here is this repository's branch, looked up the first time a line needs it.
func (bm *branchMatcher) here() string {
	if !bm.asked {
		bm.branch, bm.asked = currentBranch(bm.r), true
	}
	return bm.branch
}

// currentBranch is the branch this repository is on, empty when there is none:
// a detached HEAD, or no repository at all. Both mean nothing to match, and a
// bare marker then follows the default branch, which is what it did before.
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

// match reports the dependency's branch of this repository's name, or "" for
// the default branch. The answer is cached per module: a repository is asked
// once however many of its modules a go.mod requires.
func (bm *branchMatcher) match(mod string) string {
	if bm.here() == "" {
		return ""
	}
	if branch, asked := bm.seen[mod]; asked {
		return branch
	}
	branch := bm.probe(mod)
	bm.seen[mod] = branch
	return branch
}

// probe asks the remote for the matching branch and the default branch in one
// ls-remote. A remote that cannot answer, a dependency without the branch, and
// a dependency whose default branch IS that branch all mean the same thing:
// follow HEAD, and say so in the plain words the marker had before.
func (bm *branchMatcher) probe(mod string) string {
	ref := "refs/heads/" + bm.branch
	_, out, err := resolveGitURLAndRef(bm.r, mod, "HEAD", ref)
	if err != nil {
		return ""
	}
	refs, def := parseLsRemoteRefs(out)
	if refs[ref] == "" || def == bm.branch {
		return ""
	}
	if !jsonOutput {
		logger.Info("⇒ %s has a branch named %s too; following it rather than the default branch", mod, bm.branch)
	}
	return bm.branch
}

// branchFor is the branch a marker follows for mod: the one it names, or this
// repository's when the dependency shares it. Empty means the default branch.
func (bm *branchMatcher) branchFor(mod string, m marker) string {
	if m.branch != "" {
		return m.branch
	}
	return bm.match(mod)
}

// ref is the git ref mod resolves against under m.
func (bm *branchMatcher) ref(mod string, m marker) string {
	if branch := bm.branchFor(mod, m); branch != "" {
		return "refs/heads/" + branch
	}
	return "HEAD"
}

// describe names what a line resolved to, for a log or warning message.
func (bm *branchMatcher) describe(mod string, m marker) string {
	switch {
	case m.branch != "":
		return "branch " + m.branch
	case bm.match(mod) != "":
		return "the matching branch " + bm.branch
	default:
		return "the default branch"
	}
}
