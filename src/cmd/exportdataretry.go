package cmd

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// invalidPackageNameMarker is what go/types reports when the EXPORT DATA for an
// imported package declares an empty package name.
//
// Export data reaches the type-checker through cmd/go's build cache, which here
// is the org's shared GOCACHEPROG tier. A damaged or truncated entry still has
// a valid action key and still passes the cacheprog's content gates, so nothing
// upstream rejects it and the failure only surfaces here — as a package that
// suddenly has no name, followed by a cascade of "X redeclared in this block"
// and dozens of undefined symbols in a package the change never touched.
//
// That cascade is why this needs naming rather than retrying by hand: it reads
// exactly like a source error in someone's diff. Two runs in one session
// (host-build and build, both in src/trace) were re-run as flakes before the
// signature was recognized, and re-running a red is how a real failure
// eventually gets waved through.
//
// This is the same CLASS as modindexretry.go's corrupt module index and a
// DIFFERENT signature: that one damages a derived index and is cured by
// GODEBUG=goindex=0, which does nothing here.
const invalidPackageNameMarker = `invalid package name: ""`

// corruptExportPkgRe pulls the import paths out of the load errors, so the
// warning names what was actually damaged.
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
	seen := map[string]bool{}
	for _, m := range corruptExportPkgRe.FindAllStringSubmatch(err.Error(), -1) {
		seen[m[1]] = true
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// disableSharedBuildCache unsets GOCACHEPROG for this process and everything it
// spawns, and reports whether it was set. cmd/go then falls back to its own
// on-disk build cache, which on a fresh runner is empty — so the retry rebuilds
// the damaged packages from source instead of reading them back from the tier
// that served the damage. Correctness never depends on a build cache, so this
// costs time and nothing else.
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
