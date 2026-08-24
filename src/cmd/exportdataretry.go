package cmd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
)

// invalidPackageNameMarker: go/types' report for a damaged GOCACHEPROG export entry; see modindexretry.go's sibling signature.
const invalidPackageNameMarker = `invalid package name: ""`

// corruptExportPkgRe pulls the import paths out of load errors, so the warning names what was damaged.
var corruptExportPkgRe = regexp.MustCompile(`could not import ([^\s(]+) \(invalid package name: ""\)`)

// isCorruptExportData reports whether err is the corrupt-export-data failure.
func isCorruptExportData(err error) bool {
	return err != nil && strings.Contains(err.Error(), invalidPackageNameMarker)
}

// corruptExportPackages returns the sorted, deduplicated import paths the error
// blamed, for the operator-facing message.
func corruptExportPackages(err error) []string {
	if err == nil {
		return nil
	}
	seen := set.New[string]()
	for _, m := range corruptExportPkgRe.FindAllStringSubmatch(err.Error(), -1) {
		seen.Add(m[1])
	}
	out := seen.Values()
	sort.Strings(out)
	return out
}

// Unsets GOCACHEPROG so cmd/go falls back to its own, often-empty, on-disk cache, forcing a rebuild from source. Costs time only.
func disableSharedBuildCache() bool {
	if os.Getenv("GOCACHEPROG") == "" {
		return false
	}
	os.Unsetenv("GOCACHEPROG")
	return true
}

// corruptExportDataError wraps the unrecoverable case: either the shared cache
// was not in play, or the retry without it failed the same way. Both mean the
// damage is somewhere this cannot reach, and the run must say so plainly rather
// than leave a cascade of undefined symbols for someone to read as a source
// error.
func corruptExportDataError(err error, retried bool) error {
	pkgs := corruptExportPackages(err)
	what := "the build cache served export data with no package name"
	if len(pkgs) > 0 {
		what = fmt.Sprintf("the build cache served export data with no package name for %s", strings.Join(pkgs, ", "))
	}
	cause := "the shared build cache (GOCACHEPROG) was not enabled, so the damage is in the LOCAL build cache"
	if retried {
		cause = "rebuilding with the shared build cache (GOCACHEPROG) disabled hit the same failure, so the damage is in the LOCAL build cache"
	}
	return fmt.Errorf("%s -- this is a CORRUPT BUILD CACHE, not an error in your source. %s; clear it with `go clean -cache` and re-run: %w", what, cause, err)
}
