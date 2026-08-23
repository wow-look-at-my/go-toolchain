// Package cosmocompat closes cosmo build gaps in third-party modules that
// carry no upstream GOOS=cosmo port. See docs/COSMOCOMPAT.md for the full
// mechanism and why it lives here instead of in each consuming repo.
package cosmocompat

import "embed"

//go:embed overlay
var overlayFS embed.FS

// copySpec adds one cosmo-tagged file, built by copying an existing
// platform file from the same module and forcing its build tag to "cosmo"
// (or, if extraCond is set, "cosmo && <extraCond>" -- needed for
// race0.go/race.go in golang.org/x/sys/unix, whose own +build-race/-race
// pair is otherwise lost entirely: neither's OS-keyed condition is ever
// true under GOOS=cosmo, so a flat "cosmo" tag on both copies would make
// them collide instead of staying mutually exclusive). src and dst are
// module-relative paths.
type copySpec struct {
	src, dst, extraCond string
}

// tagEdit appends " && !cosmo" to the //go:build line of an existing
// module file, so the cosmo build doesn't also pick up a file meant for
// some other platform -- see docs/COSMOCOMPAT.md, "why the exclusions".
type tagEdit struct {
	path string
}

type copyGlob struct {
	dir, pattern string
	goos, goarch string
	extraCond    string
}

// dirMatch adds a cosmo-tagged copy of every file directly under dir whose
// EXISTING build constraint is already satisfied for goos/goarch. A
// generator that splits shared declarations across many small files -- each
// gated to whichever real platforms happen to share that code, rather than
// one file per platform -- makes a fixed copySpec list infeasible: the
// split points move on every regeneration. Matching by constraint instead
// of by filename tracks the copy automatically, so this table needs no
// update when the split changes shape, only when a genuinely new gap opens.
// archTag is ANDed onto the copy's forced tag (extraCond in addCosmoFile),
// so two dirMatch entries against the same dir -- one per real arch this
// gap supports -- never collide under a single build.
type dirMatch struct {
	dir     string
	goos    string
	goarch  string
	archTag string
}

// gap closes one third-party module's cosmo support hole. verifiedVersion
// documents the exact version this table was last verified against; a
// consumer pinning a different version is still attempted (patch and
// exclusion targets are file-existence and content-matched, so genuine
// drift fails loudly at the exact point it matters instead of on a
// coarse version-string mismatch).
type gap struct {
	module          string
	verifiedVersion string
	copies          []copySpec
	copyGlobs       []copyGlob
	dirMatches      []dirMatch
	tagEdits        []tagEdit
	// overlays maps a module-relative destination path to an embedded
	// template path under overlay/, for a file that isn't a straight copy
	// of an existing platform file (hand-written or hand-patched).
	overlays map[string]string
	// postPatch runs after copies, tagEdits, and overlays are applied, for
	// an edit to an EXISTING file this table can't express any other way
	// (golang.org/x/sys/unix's Prlimit).
	postPatch func(moduleDir string) error
}

// knownGaps is every third-party module this package knows how to patch.
// A consumer that doesn't depend on a module here is entirely unaffected --
// see Prepare.
var knownGaps = []gap{libcGap, xSysGap, sqliteGap, bubbleteaGap}
