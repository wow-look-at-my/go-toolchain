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

	// legacyBranchMarker is the original spelling. It is still READ, so an
	// unmigrated go.mod resolves correctly, and EnforceOrgBranchTracking
	// rewrites it into the form above.
	legacyBranchMarker = "go-toolchain:branch="
)

// marker is what a go.mod line's go-toolchain comment asks for.
type marker struct {
	// tracks is true when either marker above is present.
	tracks bool
	// branch names the branch to follow. Empty with tracks set means the
	// module's default branch, whatever it is called today.
	branch string
	// legacy is true for the old branch= spelling, which is what tells
	// EnforceOrgBranchTracking there is something to migrate.
	legacy bool
}

// parseMarker reads a go.mod line's tracking marker. Matched by substring, not
// prefix, so it is still found on a line combined with an "// indirect; ..."
// comment (see setIndirect in x/mod/modfile).
func parseMarker(line *modfile.Line) marker {
	if line == nil {
		return marker{}
	}
	for _, c := range line.Suffix {
		if i := strings.Index(c.Token, autoBranchMarker); i != -1 {
			m := marker{tracks: true}
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
	if m.branch == "" {
		return "the default branch"
	}
	return "branch " + m.branch
}

// comment is the marker as it is written into go.mod.
func (m marker) comment() string {
	if m.branch != "" {
		return autoBranchMarker + "=" + m.branch
	}
	return autoBranchMarker
}

// trackedBranch reports the branch a line follows, or "" when it follows no
// branch -- either because it carries no marker at all, or because it follows
// the module's default branch, which names none. Callers that have to tell
// those apart use parseMarker.
func trackedBranch(line *modfile.Line) string {
	return parseMarker(line).branch
}

// isTracked reports whether a line's version is go-toolchain's answer rather
// than somebody's choice.
func isTracked(line *modfile.Line) bool {
	return parseMarker(line).tracks
}
