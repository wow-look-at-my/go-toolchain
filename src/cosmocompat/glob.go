package cosmocompat

import (
	"fmt"
	"go/build/constraint"
	"os"
	"path/filepath"
	"strings"
)

// selectsPlatform reports whether a //go:build expression is true for
// goos/goarch. Every other tag -- a release tag like go1.24, a custom one
// like purego -- reads as false, which matches what a default build of that
// platform selects.
//
// The expression is parsed by go/build/constraint, the same package the go
// command uses, so an operator-precedence subtlety here cannot disagree with
// what the compiler will do with the copy this decision produces.
func selectsPlatform(expr, goos, goarch string) (bool, error) {
	x, err := constraint.Parse("//go:build " + expr)
	if err != nil {
		return false, fmt.Errorf("cosmocompat: parsing build constraint %q: %w", expr, err)
	}
	return x.Eval(func(tag string) bool { return tag == goos || tag == goarch }), nil
}

// buildLine returns a file's //go:build expression, and whether it has one.
// Only the leading comment block can carry it, so the scan stops at the
// package clause rather than reading whole generated files that run to
// megabytes.
func buildLine(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	for _, l := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(l, "//go:build ") {
			return strings.TrimPrefix(l, "//go:build "), true, nil
		}
		if strings.HasPrefix(l, "package ") {
			break
		}
	}
	return "", false, nil
}

// cosmoName inserts "_cosmo" before a filename's extension:
// sqlite_g_0000000000000003.go -> sqlite_g_0000000000000003_cosmo.go. The
// suffix cannot be the whole platform key the way sqlite_cosmo_amd64.go is,
// because these names are already unique and carry no platform in them.
func cosmoName(name string) string {
	ext := filepath.Ext(name)
	return strings.TrimSuffix(name, ext) + "_cosmo" + ext
}

// applyCopyGlob adds a cosmo copy of every file the glob selects. It fails
// when the glob matches nothing: a pattern that has stopped matching is the
// upstream layout changing under the table, which must be loud here rather
// than a build failure hundreds of undefined symbols long.
func applyCopyGlob(moduleOut string, g copyGlob) error {
	dir := filepath.Join(moduleOut, g.dir)
	matches, err := filepath.Glob(filepath.Join(dir, g.pattern))
	if err != nil {
		return fmt.Errorf("cosmocompat: bad glob %q: %w", g.pattern, err)
	}
	if len(matches) == 0 {
		return fmt.Errorf("cosmocompat: %s/%s matched no files (module layout may have changed upstream -- update src/cosmocompat)", g.dir, g.pattern)
	}

	selected := 0
	for _, m := range matches {
		name := filepath.Base(m)
		// Skip a copy this glob already produced, so a re-run over a
		// half-patched tree cannot copy a copy.
		if strings.Contains(name, "_cosmo.") {
			continue
		}
		expr, ok, err := buildLine(m)
		if err != nil {
			return err
		}
		if !ok {
			continue // constrained by filename alone, if at all; not ours to copy
		}
		hit, err := selectsPlatform(expr, g.goos, g.goarch)
		if err != nil {
			return err
		}
		if !hit {
			continue
		}
		spec := copySpec{
			src:       filepath.Join(g.dir, name),
			dst:       filepath.Join(g.dir, cosmoName(name)),
			extraCond: g.extraCond,
		}
		if err := addCosmoFile(moduleOut, spec); err != nil {
			return err
		}
		selected++
	}
	if selected == 0 {
		return fmt.Errorf("cosmocompat: %s/%s matched %d files but none build for %s/%s (module layout may have changed upstream -- update src/cosmocompat)",
			g.dir, g.pattern, len(matches), g.goos, g.goarch)
	}
	return nil
}
