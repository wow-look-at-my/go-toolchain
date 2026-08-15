// Package buildtags discovers the build-tag configurations a module needs so
// that no source file can hide from the pipeline behind a //go:build line.
//
// The hole this closes: vet loaded packages with default tags and `go test` ran
// with default tags, so a file carrying `//go:build sometag` was never
// type-checked, never analyzed, and its tests never ran. A failing test or a
// vet violation behind any tag was invisible to CI -- not by defeating a check,
// but by never being shown to one.
//
// Coverage is not asserted from cleverness. Configs enumerates a small set of
// tag combinations, and Verify then PROVES every taggable file was reached,
// naming any that were not. An exotic constraint the enumeration misses fails
// the build; it never silently skips.
package buildtags

import (
	"fmt"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
)

// Config is one set of build tags to run a phase under. A nil/empty Tags is the
// default configuration -- what the toolchain used to run exclusively.
type Config struct {
	Tags []string
}

// Arg renders the configuration as a `-tags` value, or "" for the default.
func (c Config) Arg() string { return strings.Join(c.Tags, ",") }

// String names the configuration for logs.
func (c Config) String() string {
	if len(c.Tags) == 0 {
		return "(default)"
	}
	return c.Arg()
}

// File records one source file that is gated behind at least one user tag, and
// therefore MUST be reached by some configuration.
type File struct {
	Path string   // path as walked, relative to the module root
	Tags []string // the user tags its constraint mentions
}

// Discovery is the result of scanning a module.
type Discovery struct {
	// Configs are the tag sets to run every phase under, default first.
	Configs []Config
	// Gated are the files that only build under some non-default tag set.
	Gated []File
	// UserTags is every user-defined tag found, sorted.
	UserTags []string
}

// platformIdents are constraint identifiers that describe the BUILD TARGET
// rather than a project's own opt-in. A file gated only by these is excluded on
// this host for a reason the pipeline cannot and should not override -- vetting
// a windows-only file on linux is not what this package is for. Every other
// identifier is treated as a user tag, so an unknown one fails safe by being
// covered rather than skipped.
var platformIdents = map[string]bool{
	"cgo": true, "race": true, "msan": true, "asan": true,
	"gc": true, "gccgo": true, "unix": true, "boringcrypto": true,
	"purego": true, "netgo": true, "osusergo": true, "timetzdata": true,
	"ignore": false, // deliberately NOT a platform ident: see package doc
}

// knownOS and knownArch cover the GOOS/GOARCH values that may appear as
// constraint idents. Sourced from `go tool dist list`; a value missing here is
// treated as a user tag, which over-covers rather than under-covers.
var knownOS = set.Of(
	// cosmo is the gosmopolitan fork's GOOS, so `go tool dist list` does not
	// name it. It is a build target all the same, and one this host cannot
	// stand in for: a `-tags cosmo` load here still satisfies every _linux.go
	// filename constraint, so each cosmo variant collides with its linux
	// sibling ("socketPeerPID redeclared"). The GOOS=cosmo matrix job compiles
	// these files with the fork's toolchain, which is where they are checkable.
	"cosmo",
	"aix", "android", "darwin", "dragonfly",
	"freebsd", "hurd", "illumos", "ios", "js",
	"linux", "nacl", "netbsd", "openbsd", "plan9",
	"solaris", "wasip1", "windows", "zos",
)

var knownArch = set.Of(
	"386", "amd64", "amd64p32", "arm", "arm64",
	"arm64be", "armbe", "loong64", "mips",
	"mips64", "mips64le", "mips64p32", "mips64p32le",
	"mipsle", "ppc", "ppc64", "ppc64le", "riscv",
	"riscv64", "s390", "s390x", "sparc", "sparc64",
	"wasm",
)

// isPlatformIdent reports whether ident describes the build target rather than
// a project opt-in.
func isPlatformIdent(ident string) bool {
	if v, ok := platformIdents[ident]; ok {
		return v
	}
	if knownOS.Contains(ident) || knownArch.Contains(ident) {
		return true
	}
	// go1.N release tags.
	return strings.HasPrefix(ident, "go1.")
}

// skipDir reports directory names the Go tool itself never builds from, so a
// constraint inside one is not a pipeline gap.
func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" || name == "node_modules" ||
		(len(name) > 1 && (name[0] == '.' || name[0] == '_'))
}

// Scan walks the module rooted at dir and returns the configurations every
// phase must run under, plus the gated files that prove they were needed.
func Scan(dir string) (*Discovery, error) {
	tagSet := map[string]bool{}
	var gated []File

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != dir && skipDir(d.Name()) {
				return fs.SkipDir
			}
			// A nested module's packages are not import paths of this one, so
			// a pattern naming one fails to load ("main module does not
			// contain package ..."). Its tags are its own module's business:
			// src/compat/go-isatty carries `appengine`, which showed up here
			// as a configuration this module was then asked to vet itself
			// under -- and could not.
			if path != dir && gomod.IsNestedModule(path) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		tags, err := fileTags(path)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if len(tags) == 0 {
			return nil
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		for _, t := range tags {
			tagSet[t] = true
		}
		gated = append(gated, File{Path: filepath.ToSlash(rel), Tags: tags})
		return nil
	})
	if err != nil {
		return nil, err
	}

	userTags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		userTags = append(userTags, t)
	}
	sort.Strings(userTags)
	sort.Slice(gated, func(i, j int) bool { return gated[i].Path < gated[j].Path })

	return &Discovery{Configs: configsFor(userTags), Gated: gated, UserTags: userTags}, nil
}

// configsFor builds the tag sets to run. The default comes first so the
// pipeline's primary output is unchanged; then each tag alone, which satisfies
// any `a && !b` shape; then all of them together, which satisfies `a && b`.
// Whether that is sufficient is never assumed -- Verify checks it.
func configsFor(userTags []string) []Config {
	configs := []Config{{}}
	if len(userTags) == 0 {
		return configs
	}
	for _, t := range userTags {
		configs = append(configs, Config{Tags: []string{t}})
	}
	if len(userTags) > 1 {
		all := append([]string(nil), userTags...)
		configs = append(configs, Config{Tags: all})
	}
	return configs
}

// fileTags returns the user tags a file's build constraint mentions. A file
// with no constraint, or one written purely in platform terms, returns nil.
func fileTags(path string) ([]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.PackageClauseOnly|parser.ParseComments)
	if err != nil {
		// A file that does not parse is not a tag problem; the type-check
		// phase reports it with a far better message than this walk could.
		return nil, nil //nolint:nilerr // reported downstream, not here
	}
	seen := map[string]bool{}
	var tags []string
	for _, group := range f.Comments {
		// Constraints must precede the package clause.
		if group.Pos() > f.Package {
			break
		}
		for _, c := range group.List {
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				continue // not a build constraint
			}
			walkConstraint(expr, func(ident string) {
				if isPlatformIdent(ident) || seen[ident] {
					return
				}
				seen[ident] = true
				tags = append(tags, ident)
			})
		}
	}
	// Filename-based constraints (foo_windows.go, foo_amd64.go) are platform
	// only, so nothing to add: they are handled by isPlatformIdent's callers
	// never seeing them.
	sort.Strings(tags)
	return tags, nil
}

// walkConstraint visits every identifier in a parsed build expression.
func walkConstraint(e constraint.Expr, visit func(string)) {
	switch x := e.(type) {
	case *constraint.TagExpr:
		visit(x.Tag)
	case *constraint.NotExpr:
		walkConstraint(x.X, visit)
	case *constraint.AndExpr:
		walkConstraint(x.X, visit)
		walkConstraint(x.Y, visit)
	case *constraint.OrExpr:
		walkConstraint(x.X, visit)
		walkConstraint(x.Y, visit)
	}
}

// GatedPatterns returns the package patterns the EXTRA configurations need to
// cover -- the directories holding gated files, as `./dir` patterns -- or nil
// when nothing is gated.
//
// The default configuration always runs over the whole module. The extra ones
// exist solely to reach files the default cannot, so running them over `./...`
// would re-analyze and re-run the entire tree once per tag for no added
// coverage. On a module with three tags that is four redundant full test runs.
// Verify still checks the outcome, so narrowing the pattern cannot narrow the
// guarantee: a gated file outside these directories fails the run.
func (d *Discovery) GatedPatterns() []string {
	seen := map[string]bool{}
	var pats []string
	for _, f := range d.Gated {
		dir := path.Dir(f.Path)
		if dir == "." {
			dir = "./"
		} else {
			dir = "./" + dir
		}
		if seen[dir] {
			continue
		}
		seen[dir] = true
		pats = append(pats, dir)
	}
	sort.Strings(pats)
	return pats
}

// Verify reports the gated files that no configuration actually reached.
//
// This is the guarantee. Configs' enumeration is a heuristic; this check is
// not. seen is the set of file paths (relative to the module root, slash
// separated) that the phase genuinely analyzed or compiled across every
// configuration. Any gated file missing from it is a file the pipeline cannot
// see, and the caller must FAIL rather than continue -- otherwise the tag is
// exactly the bypass this package exists to remove.
func Verify(d *Discovery, seen set.Set[string]) []File {
	var missed []File
	for _, f := range d.Gated {
		if !seen.Contains(f.Path) {
			missed = append(missed, f)
		}
	}
	return missed
}

// UnreachableError renders the missed files as an error a human can act on.
func UnreachableError(missed []File, phase string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s could not reach %d build-tagged file(s); "+
		"a tag must never hide code from the pipeline:", phase, len(missed))
	for _, f := range missed {
		fmt.Fprintf(&b, "\n  %s  (tags: %s)", f.Path, strings.Join(f.Tags, ", "))
	}
	b.WriteString("\n\nEvery //go:build tag must be reachable by one of the " +
		"configurations buildtags.Scan derives. If this file's constraint is " +
		"deliberately unsatisfiable, delete the file instead of leaving " +
		"unchecked code in the tree.")
	return fmt.Errorf("%s", b.String())
}

// HostPlatform names the GOOS/GOARCH this process runs as, for messages that
// need to explain why a platform-gated file was not considered.
func HostPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }
