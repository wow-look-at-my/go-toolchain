package cmd

import (
	"fmt"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// staleAnalyzerMarkers are what go/types reports when it is asked to build a
// type it does not know about, because the EXPORT DATA it is reading came from
// a NEWER toolchain than the one that compiled this binary.
//
// Export data is forward-compatible and not backward-compatible: a Go 1.27
// compiler writes constructs a Go 1.26 go/types has no representation for. The
// vet loader type-checks with the go/types LINKED INTO this binary, so the two
// versions have to move together, and a released binary pins its half.
//
// Go 1.27's generic methods are the first construct to hit this. go/types
// panicked on a signature with both a receiver and type parameters right up to
// Go 1.26; x/tools recovers that panic and reports it as an internal importer
// error, which reads like a bug in the imported package rather than a version
// skew. That is why this needs naming: nothing in the message mentions a
// version, so the reader has no reason to suspect one.
var staleAnalyzerMarkers = []string{
	// Go <= 1.26 go/types, reading a Go 1.27 generic method.
	"function with type parameters cannot have a receiver",
}

// staleAnalyzerPkgRe pulls the import paths out of the load errors, so the
// message names what could not be read.
var staleAnalyzerPkgRe = regexp.MustCompile(`could not import ([^\s(]+) \(`)

// isStaleAnalyzer reports whether err is the newer-export-data failure.
func isStaleAnalyzer(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, m := range staleAnalyzerMarkers {
		if strings.Contains(msg, m) {
			return true
		}
	}
	return false
}

// staleAnalyzerPackages returns the sorted, deduplicated import paths the error
// blamed.
func staleAnalyzerPackages(err error) []string {
	if err == nil {
		return nil
	}
	seen := set.New[string]()
	for _, m := range staleAnalyzerPkgRe.FindAllStringSubmatch(err.Error(), -1) {
		seen.Add(m[1])
	}
	out := seen.Values()
	sort.Strings(out)
	return out
}

// staleAnalyzerError explains the version skew and names both halves of it.
//
// goVersion is the toolchain running the build (the one that WROTE the export
// data); runtime.Version() is the toolchain that compiled this binary (the one
// that must READ it). Rebuilding go-toolchain with the first is the repair, and
// on a released binary that means updating it.
func staleAnalyzerError(err error, goVersion string) error {
	built := runtime.Version()
	what := "vet could not read the export data of a package"
	if pkgs := staleAnalyzerPackages(err); len(pkgs) > 0 {
		what = fmt.Sprintf("vet could not read the export data of %s", strings.Join(pkgs, ", "))
	}
	using := goVersion
	if using == "" {
		using = "the toolchain in use"
	}
	return fmt.Errorf("%s -- this is a TOOLCHAIN VERSION SKEW, not an error in your source. "+
		"go-toolchain was built with %s, the build is running %s, and export data is not readable by an older go/types. "+
		"Update go-toolchain (it is built against the Go version in its own go.mod), or build with %s: %w",
		what, built, using, built, err)
}
