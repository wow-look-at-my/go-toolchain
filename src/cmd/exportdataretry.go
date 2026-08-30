package cmd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// The vet type-check reads a dependency's EXPORT DATA -- its compiled API --
// instead of its source. The markers below say that data did not decode, and
// differ by how far the decode got. Depth: docs/CI.md
const (
	// The header is unreadable, so the package has no name to report.
	invalidPackageNameMarker = `invalid package name: ""`
	// The header decoded and the type graph inside it did not.
	internalImportErrorMarker = "internal error in importing"
)

// exportPkgRe pulls the import paths out of load errors, so the warning names what could not be read.
var exportPkgRe = regexp.MustCompile(`could not import ([^\s(]+) \([^)]*(?:invalid package name: ""|internal error in importing)`)

// exportDataSignature returns the marker err carries, or "" when it is not this
// failure. The caller reports it, so the reader knows which marker matched.
func exportDataSignature(err error) string {
	if err == nil {
		return ""
	}
	for _, marker := range []string{invalidPackageNameMarker, internalImportErrorMarker} {
		if strings.Contains(err.Error(), marker) {
			return marker
		}
	}
	return ""
}

// isUnreadableExportData reports whether err is the unreadable-export-data failure.
func isUnreadableExportData(err error) bool {
	return exportDataSignature(err) != ""
}

// unreadableExportPackages returns the sorted, deduplicated import paths the
// error blamed, for the operator-facing message.
func unreadableExportPackages(err error) []string {
	if err == nil {
		return nil
	}
	seen := set.New[string]()
	for _, m := range exportPkgRe.FindAllStringSubmatch(err.Error(), -1) {
		seen.Add(m[1])
	}
	out := seen.Values()
	sort.Strings(out)
	return out
}

// Unsets GOCACHEPROG so cmd/go falls back to its own on-disk cache. Costs time only.
func disableSharedBuildCache() bool {
	if os.Getenv("GOCACHEPROG") == "" {
		return false
	}
	os.Unsetenv("GOCACHEPROG")
	return true
}

// unreadableExportDataError wraps what the source retry could not clear. That
// retry reads no export data at all, so a repeat rules out both cures below,
// and the run must say so rather than leave a cascade of undefined symbols for
// someone to read as a source error.
func unreadableExportDataError(err error) error {
	pkgs := unreadableExportPackages(err)
	what := fmt.Sprintf("the type-check could not read the compiler's export data (%s)", exportDataSignature(err))
	if len(pkgs) > 0 {
		what = fmt.Sprintf("%s for %s", what, strings.Join(pkgs, ", "))
	}
	return fmt.Errorf("%s -- that is a dependency's compiled API, NOT your source. The retry type-checked every dependency from source, which reads no export data, and hit this again: neither a damaged cache entry (`go clean -cache`) nor an importer older than the toolchain explains it: %w", what, err)
}
