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
var corruptExportPkgRe = regexp.MustCompile(`could not import ([^\s(]+) \((?:invalid package name: ""|open [^)]*: no such file or directory)\)`)

// missingExportFileRe: a GET answers a hit with a DiskPath, and the compiler could not open it. Eviction behind the promise is one way (cache/packverify.go); a cache directory another process mutates is another.
var missingExportFileRe = regexp.MustCompile(`could not import [^\s(]+ \(open [^)]*: no such file or directory\)`)

// isCorruptExportData reports whether err is a cache-served export failure.
func isCorruptExportData(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), invalidPackageNameMarker) || missingExportFileRe.MatchString(err.Error())
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

// corruptExportSymptom names how the entry was unusable, so the message tells a reader which failure they have.
func corruptExportSymptom(err error) string {
	if err != nil && missingExportFileRe.MatchString(err.Error()) {
		return "an export file that is no longer there"
	}
	return "export data with no package name"
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
	what := "the build cache served " + corruptExportSymptom(err)
	if len(pkgs) > 0 {
		what = fmt.Sprintf("the build cache served %s for %s", corruptExportSymptom(err), strings.Join(pkgs, ", "))
	}
	cause := "the shared build cache (GOCACHEPROG) was not enabled, so the damage is in the LOCAL build cache"
	if retried {
		cause = "rebuilding with the shared build cache (GOCACHEPROG) disabled hit the same failure, so the damage is in the LOCAL build cache"
	}
	return fmt.Errorf("%s -- this is a CORRUPT BUILD CACHE, not an error in your source. %s; clear it with `go clean -cache` and re-run: %w", what, cause, err)
}
