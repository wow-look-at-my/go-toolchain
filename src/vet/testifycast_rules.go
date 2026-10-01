package vet

import (
	"bytes"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"math"
	"os"
	"sort"
	"strings"

	ansi "github.com/wow-look-at-my/ansi-writer"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// constRepresentable reports whether constant v can be represented exactly in
// basic type b. Used to refuse conversions that would truncate or overflow a
// constant — the fork compared the underlying numeric values, so such a pair
// was never equal and we must not paper over the inequality.
func constRepresentable(v constant.Value, b *types.Basic) bool {
	if v == nil || v.Kind() == constant.Unknown {
		return false
	}
	switch {
	case b.Info()&types.IsInteger != 0:
		iv := constant.ToInt(v)
		if iv.Kind() != constant.Int {
			return false // not an integral value (a fraction)
		}
		return intInRange(iv, b.Kind())
	case b.Info()&types.IsFloat != 0:
		fv := constant.ToFloat(v)
		if fv.Kind() == constant.Unknown {
			return false
		}
		f, _ := constant.Float64Val(fv)
		if math.IsInf(f, 0) {
			return false // overflows float64
		}
		if b.Kind() == types.Float32 && math.IsInf(float64(float32(f)), 0) {
			return false // value is finite in float64 but overflows float32
		}
		return true
	default:
		return false
	}
}

// intInRange reports whether the integer constant v fits in the integer basic
// kind k. types.Int/Uint/Uintptr are treated as word-width, matching the analysis
// host and the platforms go-toolchain targets.
func intInRange(v constant.Value, k types.BasicKind) bool {
	switch k {
	case types.Int, types.Int64:
		_, ok := constant.Int64Val(v)
		return ok
	case types.Int8:
		i, ok := constant.Int64Val(v)
		return ok && i >= math.MinInt8 && i <= math.MaxInt8
	case types.Int16:
		i, ok := constant.Int64Val(v)
		return ok && i >= math.MinInt16 && i <= math.MaxInt16
	case types.Int32: // includes rune
		i, ok := constant.Int64Val(v)
		return ok && i >= math.MinInt32 && i <= math.MaxInt32
	case types.Uint, types.Uint64, types.Uintptr:
		if constant.Sign(v) < 0 {
			return false
		}
		_, ok := constant.Uint64Val(v)
		return ok
	case types.Uint8: // includes byte
		u, ok := constant.Uint64Val(v)
		return ok && constant.Sign(v) >= 0 && u <= math.MaxUint8
	case types.Uint16:
		u, ok := constant.Uint64Val(v)
		return ok && constant.Sign(v) >= 0 && u <= math.MaxUint16
	case types.Uint32:
		u, ok := constant.Uint64Val(v)
		return ok && constant.Sign(v) >= 0 && u <= math.MaxUint32
	}
	return false
}

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
	// For collection-vs-collection asserts, compare element types; for value-in-collection, the member operand is a scalar.
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

// neededImports returns the sorted union of the import paths the edits require
// (recorded per edit when a conversion names a package the file doesn't import).
func (c *CastEdits) neededImports() []string {
	seen := set.New[string]()
	var paths []string
	for _, e := range c.Edits {
		for _, p := range e.AddImports {
			if seen.Add(p) {
				paths = append(paths, p)
			}
		}
	}
	sort.Strings(paths)
	return paths
}

// addImportsToSource returns src with the given import paths added to its
// import declaration. Without the import an inserted conversion like
// wrapping an untyped constant in fs.FileMode would not compile, and the load
// error blocks every later vet run. This reprints the whole file (parse, astutil.AddImport,
// go/printer), so like every AST-reprinting fixer it emits through
// canonicalizeGoSource.
func addImportsToSource(src []byte, filename string, paths []string) ([]byte, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	for _, p := range paths {
		astutil.AddImport(fset, f, p)
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, f); err != nil {
		return nil, err
	}
	return canonicalizeGoSource(buf.Bytes()), nil
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
	want := c.rendered(src)
	if paths := c.neededImports(); len(paths) > 0 {
		want, err = addImportsToSource(want, c.Filename, paths)
		if err != nil {
			return false, err
		}
	}
	wrote, err := ed.Require(c.Filename, want, "testify Equal/NotEqual/Greater/Less needs explicit type conversions for upstream testify")
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
