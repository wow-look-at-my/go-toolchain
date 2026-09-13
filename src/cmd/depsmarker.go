package cmd

import (
	"strings"

	"golang.org/x/mod/modfile"
)

// The trailing comments that make a line's version an ANSWER rather than a
// choice. Each rides either the require line or, for a fork consumed through a
// replacement, the replace line.
const (
	// autoBranchMarker follows the module's DEFAULT branch (bare) or a named branch via "=<branch>"; resolved every run.
	autoBranchMarker = "go-toolchain:auto-branch"

	// legacyBranchMarker is the old spelling; still read, and EnforceOrgBranchTracking migrates it to the form above.
	legacyBranchMarker = "go-toolchain:branch="

	// generateMarker records the approved hash of a module's go:generate directives, on its require line or its module line.
	generateMarker = "go-toolchain:generate="
)

// parseGenerateMarker reads the approved generate hash off a go.mod line, or ""
// when the line records none. Matched by substring, like parseMarker, so it is
// found beside an indirect comment or a tracking marker.
func parseGenerateMarker(line *modfile.Line) string {
	if line == nil {
		return ""
	}
	for _, c := range line.Suffix {
		if i := strings.Index(c.Token, generateMarker); i != -1 {
			return strings.TrimRight(markerValue(c.Token[i+len(generateMarker):]), ";")
		}
	}
	return ""
}

// setGenerateMarker replaces any generate approval on a line with hash, joined
// to an existing comment the way setMarker joins a tracking marker.
func setGenerateMarker(line *modfile.Line, hash string) {
	kept := line.Suffix[:0]
	for _, c := range line.Suffix {
		token := stripMarks(c.Token, generateMarker)
		if token == "" {
			continue
		}
		c.Token = token
		kept = append(kept, c)
	}
	line.Suffix = kept
	mark := generateMarker + hash
	if len(line.Suffix) == 0 {
		line.Suffix = []modfile.Comment{{Token: "// " + mark, Suffix: true}}
		return
	}
	line.Suffix[0].Token += "; " + mark
}

// lineText spells a go.mod line as it reads inside its block, comment included.
func lineText(line *modfile.Line) string {
	parts := append([]string(nil), line.Token...)
	for _, c := range line.Suffix {
		parts = append(parts, c.Token)
	}
	return strings.Join(parts, " ")
}

// marker is what a go.mod line's go-toolchain comment asks for.
type marker struct {
	// tracks is true when either marker above is present.
	tracks bool
	// branch names the branch to follow; empty with tracks set means the module's current default branch.
	branch string
	// legacy is true for the old branch= spelling, telling EnforceOrgBranchTracking there is something to migrate.
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
// up to the leading space, so a trailing note stays readable.
func markerValue(rest string) string {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// meaning names what the COMMENT asks for. What a bare marker RESOLVES to depends
// on the dependency, and branchMatcher.describe names that.
func (m marker) meaning() string {
	if m.branch == "" {
		return "a branch of this repository's name, or the default branch"
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

// trackedBranch reports the followed branch, or "" for no marker or a default branch.
// Callers telling those apart use parseMarker.
func trackedBranch(line *modfile.Line) string {
	return parseMarker(line).branch
}

// isTracked reports whether a line's version is go-toolchain's answer rather
// than somebody's choice.
func isTracked(line *modfile.Line) bool {
	return parseMarker(line).tracks
}
