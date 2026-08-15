package cmd

import (
	"strings"

	"golang.org/x/mod/modfile"
)

// The trailing comments that make a line's version an ANSWER rather than a
// choice. Each rides either the require line or, for a fork consumed through a
// replacement, the replace line.
const (
	// autoBranchMarker follows the module's DEFAULT branch, resolved on every
	// run. Bare, it names no branch at all -- which is the point: a branch's
	// name lives on the remote, and a copy of it in go.mod is one more thing
	// that goes stale the day the default branch is renamed. "=<branch>" names
	// a different branch deliberately.
	//
	//	require github.com/wow-look-at-my/foo v0.0.0-... // go-toolchain:auto-branch
	//	require github.com/wow-look-at-my/bar v0.0.0-... // go-toolchain:auto-branch=v1
	autoBranchMarker = "go-toolchain:auto-branch"

	// siblingMarker matches the commit another module resolved to. It is
	// written by go-toolchain and not by hand: which modules share a repository
	// is read off that repository, so a hand-written one is a guess.
	//
	//	require github.com/wow-look-at-my/foo/go/core v0.0.0-... // go-toolchain:sibling=github.com/wow-look-at-my/foo/go/client
	siblingMarker = "go-toolchain:sibling="

	// legacyBranchMarker is the original spelling. It is still READ, so an
	// unmigrated go.mod resolves correctly, and EnforceOrgBranchTracking
	// rewrites it into the form above.
	legacyBranchMarker = "go-toolchain:branch="
)

// marker is what a go.mod line's go-toolchain comment asks for.
type marker struct {
	// tracks is true when any of the markers above is present.
	tracks bool
	// branch names the branch to follow. Empty with tracks set means the
	// module's default branch, whatever it is called today.
	branch string
	// sibling names the module whose commit this line matches, and is empty
	// on a line that follows a branch.
	sibling string
	// legacy is true for the old branch= spelling, which is what tells
	// EnforceOrgBranchTracking there is something to migrate.
	legacy bool
	// compat is the branch name written for readers that predate this
	// marker. See comment.
	compat string
}

// parseMarker reads a go.mod line's tracking marker. Matched by substring, not
// prefix, so it is still found on a line combined with an "// indirect; ..."
// comment (see setIndirect in x/mod/modfile).
func parseMarker(line *modfile.Line) marker {
	if line == nil {
		return marker{}
	}
	for _, c := range line.Suffix {
		if i := strings.Index(c.Token, siblingMarker); i != -1 {
			return marker{tracks: true, sibling: markerValue(c.Token[i+len(siblingMarker):]), compat: compatBranch(c.Token)}
		}
		if i := strings.Index(c.Token, autoBranchMarker); i != -1 {
			m := marker{tracks: true, compat: compatBranch(c.Token)}
			if named, ok := strings.CutPrefix(c.Token[i+len(autoBranchMarker):], "="); ok {
				m.branch = markerValue(named)
			}
			return m
		}
		if i := strings.Index(c.Token, legacyBranchMarker); i != -1 {
			return marker{tracks: true, branch: markerValue(c.Token[i+len(legacyBranchMarker):]), legacy: true}
		}
	}
	return marker{}
}

// compatBranch reads the branch name from the compatibility half of a marker
// comment, or "" when the comment carries none.
func compatBranch(token string) string {
	i := strings.Index(token, legacyBranchMarker)
	if i == -1 {
		return ""
	}
	return markerValue(token[i+len(legacyBranchMarker):])
}

// markerValue takes a marker's value off the front of the rest of a comment:
// up to the first space, so a trailing note stays readable.
func markerValue(rest string) string {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// ref is the git ref this marker resolves against. A marker naming no branch
// resolves HEAD, which IS the default branch and costs no extra lookup to
// follow.
func (m marker) ref() string {
	if m.branch == "" {
		return "HEAD"
	}
	return "refs/heads/" + m.branch
}

// describe names what a line follows, for a log or warning message.
func (m marker) describe() string {
	switch {
	case m.sibling != "":
		return "the commit of " + m.sibling
	case m.branch == "":
		return "the default branch"
	default:
		return "branch " + m.branch
	}
}

// comment is the marker as it is written into go.mod, with a compatibility
// half appended when the caller supplied one:
//
//	// go-toolchain:auto-branch go-toolchain:branch=master
//
// A go-toolchain release that predates these markers looks for
// "go-toolchain:branch=" and takes EVERYTHING after it as the branch name. So
// the legacy spelling goes LAST and nothing follows it, and both readers get a
// right answer off one line: an old one follows the named branch, this one
// follows the marker in front and ignores the rest. Neither rewrites the
// other's work, which is what stops a mixed fleet from fighting over go.mod --
// and what let this marker ship at all, since an unrecognized one is not
// ignored by an old release, it is overwritten with a comment of its own on a
// line of its own, which corrupts the require block.
//
// The compatibility half is redundant the moment every runner reads the marker
// in front of it, and a later release drops it.
func (m marker) comment() string {
	out := autoBranchMarker
	switch {
	case m.sibling != "":
		out = siblingMarker + m.sibling
	case m.branch != "":
		out = autoBranchMarker + "=" + m.branch
	}
	if m.compat != "" {
		out += " " + legacyBranchMarker + m.compat
	}
	return out
}

// trackedBranch reports the branch a line follows, or "" when it follows no
// branch -- either because it carries no marker at all, or because it follows
// a default branch or a sibling, neither of which names one. Callers that have
// to tell those apart use parseMarker.
func trackedBranch(line *modfile.Line) string {
	return parseMarker(line).branch
}

// isTracked reports whether a line's version is go-toolchain's answer rather
// than somebody's choice.
func isTracked(line *modfile.Line) bool {
	return parseMarker(line).tracks
}
