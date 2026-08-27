package sliceset

import (
	"slices"
	"strings"
)

// A literal spelled inside the lookup exists for that one question.
func inlineLiteral(name string) bool {
	return slices.Contains([]string{"linux", "darwin"}, name) // want `a slice literal answering one membership question is a set`
}

// A slice this package builds and only asks membership of is a set.
func builtThenAsked(names []string, want string) bool {
	seen := make([]string, 0) // want `a slice is only ever used as a set`
	for _, n := range names {
		seen = append(seen, n)
	}
	return slices.Contains(seen, want) && len(seen) > 0
}

var knownHosts = []string{"github.com", "gitlab.com"} // want `a slice the package only asks membership of is a set`

func known(host string) bool { return slices.Contains(knownHosts, host) }

// Writing the scan by hand asks the same question.
func handRolled(want string) bool {
	pkgLevel := []string{"a", "b"} // want `a slice the package only asks membership of is a set`
	for _, v := range pkgLevel {
		if v == want {
			return true
		}
	}
	return false
}

// Appending only what the scan says is absent is a set insert, whatever the
// slice does afterwards.
func dedupe(in []string) []string {
	var out []string
	for _, v := range in {
		if !slices.Contains(out, v) { // want `appending only what a scan says is absent is a set insert`
			out = append(out, v)
		}
	}
	return out
}

// A slice the code reads by position keeps its order.
func indexed(names []string) string {
	ordered := make([]string, 0)
	ordered = append(ordered, names...)
	if slices.Contains(ordered, "x") {
		return ordered[0]
	}
	return ""
}

// A slice handed to another function can be read any way at all.
func joined(want string) string {
	valid := []string{"a", "b"}
	if slices.Contains(valid, want) {
		return want
	}
	return strings.Join(valid, ", ")
}

// The position itself is the answer here, not membership.
func position(want string) int {
	order := []string{"a", "b"}
	return slices.Index(order, want)
}

// A slice that arrives from somewhere else belongs to its caller.
func fromCaller(names []string, want string) bool {
	return slices.Contains(names, want)
}

// A byte slice is a buffer.
func inBuffer(want byte) bool {
	buf := make([]byte, 0)
	buf = append(buf, 'a')
	return slices.Contains(buf, want)
}

// A slice nobody asks membership of is a list.
func plainList(names []string) int {
	out := make([]string, 0)
	for _, n := range names {
		out = append(out, n)
	}
	return len(out)
}
