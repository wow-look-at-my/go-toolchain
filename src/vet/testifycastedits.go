package vet

import (
	"go/ast"
	"go/types"
	"os"
	"sort"
	"strings"

	ansi "github.com/wow-look-at-my/ansi-writer"
	"golang.org/x/tools/go/analysis"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// warnElementMismatch prints a non-fatal warning when an element-comparison
// assertion (Contains, ElementsMatch, Subset, ...) compares a collection whose
// element type differs from the value being searched for. Under the fork these
// could match via loose numeric equality; we don't rewrite them, but we surface
// the case so the behavior change isn't silent.
func warnElementMismatch(pass *analysis.Pass, call *ast.CallExpr, name string, collIdx, valIdx int) {
	collTV, ok1 := pass.TypesInfo.Types[call.Args[collIdx]]
	valTV, ok2 := pass.TypesInfo.Types[call.Args[valIdx]]
	if !ok1 || !ok2 || collTV.Type == nil || valTV.Type == nil {
		return
	}
	elem := elementType(collTV.Type)
	if elem == nil {
		return
	}
	// For collection-vs-collection assertions (ElementsMatch/Subset/NotSubset)
	// the second operand is itself a collection, so compare its element type;
	// for value-in-collection (Contains) the second operand is a scalar value.
	cmpType := types.Default(valTV.Type)
	if e2 := elementType(valTV.Type); e2 != nil {
		cmpType = types.Default(e2)
	}
	if types.Identical(types.Default(elem), cmpType) {
		return
	}
	eb, ok1 := types.Default(elem).Underlying().(*types.Basic)
	vb, ok2 := cmpType.Underlying().(*types.Basic)
	if !ok1 || !ok2 || !isForkNumeric(eb) || !isForkNumeric(vb) {
		return
	}
	pos := pass.Fset.Position(call.Pos())
	logger.Warn(
		"testifycast: warning: %s:%d: %s compares %s elements against %s; the testify fork may have matched these via loose numeric equality, but upstream will not — add an explicit conversion if a match was intended",
		pos.Filename, pos.Line, name, elem, cmpType)
}

// elementType returns the element type of a slice, array, or map value type, or
// nil if the type isn't a collection.
func elementType(t types.Type) types.Type {
	switch u := t.Underlying().(type) {
	case *types.Slice:
		return u.Elem()
	case *types.Array:
		return u.Elem()
	case *types.Map:
		return u.Elem()
	}
	return nil
}

// importsUpstreamTestify reports whether file imports an upstream testify
// package, used as a cheap filter before walking the file.
func importsUpstreamTestify(file *ast.File) bool {
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == upstreamAssertPkg || path == upstreamRequirePkg {
			return true
		}
	}
	return false
}

// rendered returns the file's source with all edits applied, without writing to
// disk. Edits are applied right-to-left so byte offsets stay valid.
func (c *CastEdits) rendered(src []byte) []byte {
	edits := append([]CastEdit(nil), c.Edits...)
	sort.Slice(edits, func(i, j int) bool { return edits[i].Start > edits[j].Start })

	out := src
	for _, e := range edits {
		s := c.Fset.Position(e.Start).Offset
		en := c.Fset.Position(e.End).Offset
		if s < 0 || en > len(out) || s > en {
			continue
		}
		var b []byte
		b = append(b, out[:s]...)
		b = append(b, e.TypeName...)
		b = append(b, '(')
		b = append(b, out[s:en]...)
		b = append(b, ')')
		b = append(b, out[en:]...)
		out = b
	}
	return out
}

// Apply routes the file with all edits applied through ed: a fix-mode editor
// rewrites it on disk (and a fix line is printed per edit), a check-mode (CI)
// editor records a violation. testifycast emits no analyzer diagnostic of its
// own, so this recorded violation is what fails CI. Returns whether it wrote.
func (c *CastEdits) Apply(ed Editor) (bool, error) {
	src, err := os.ReadFile(c.Filename)
	if err != nil {
		return false, err
	}
	wrote, err := ed.Require(c.Filename, c.rendered(src), "testify Equal/NotEqual/Greater/Less needs explicit type conversions for upstream testify")
	if err != nil {
		return false, err
	}
	if wrote {
		for _, e := range c.Edits {
			c.printEdit(src, e)
		}
	}
	return wrote, nil
}

// printEdit prints a colored old -> new line for a single conversion.
func (c *CastEdits) printEdit(src []byte, e CastEdit) {
	pos := c.Fset.Position(e.Start)
	loc := SourceLocation{File: pos.Filename, Line: pos.Line, Column: pos.Column}
	s := c.Fset.Position(e.Start).Offset
	en := c.Fset.Position(e.End).Offset
	old := ""
	if s >= 0 && en <= len(src) && s <= en {
		old = string(src[s:en])
	}
	yellow := ansi.Concat(ansi.Yellow.FG, "fixed:", ansi.Reset)
	grey := ansi.Concat(ansi.BrightBlack.FG, loc.ShortLoc(), ansi.Reset)
	red := ansi.Concat(ansi.Red.FG, old, ansi.Reset)
	green := ansi.Concat(ansi.Green.FG, e.TypeName+"("+old+")", ansi.Reset)
	logger.Info("%s %s %s → %s", yellow, grey, red, green)
}
