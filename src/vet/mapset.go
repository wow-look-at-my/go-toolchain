package vet

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"
	"sync"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/tools/go/analysis"
)

// MapSetAnalyzer reports a map[K]bool that carries no information in its
// values, and so is a set spelled as a map. Two shapes are reported:
//
//   - a composite literal whose every value is the constant true. A literal
//     that writes one false answers a question and stays.
//   - a variable made with make(map[K]bool) whose every use in the package is
//     a set operation: a write of true, delete, clear, len, a key-only range,
//     or an index read. See mapSetUses for what disqualifies a candidate.
//
// The remedy is github.com/wow-look-at-my/go-containers/set. Its Set[T] holds
// the membership operations a map spells out by hand: Contains, ContainsAll,
// Union, Intersection, Difference, and the subset predicates.
//
// A map[K]struct{} gets a WARNING instead, never a diagnostic. It already
// carries no value, so it is not the mistake this check fails a build over.
// The default a map[K]bool picks is. The set package itself is exempt from
// that warning: Set[T] IS the map[T]struct{} the warning points at.
//
// Severity follows the module. An org module has the remedy one require away,
// so the map[K]bool findings FAIL its build. In anybody else's module the same
// findings are warnings: the code is just as wasteful, but the fix would add a
// dependency its author never chose, and that is their call to make.
//
// Depth: docs/VET.md
var MapSetAnalyzer = &analysis.Analyzer{
	Name:       "mapset",
	Doc:        "detects a map[K]bool used as a set; use github.com/wow-look-at-my/go-containers/set instead",
	Run:        runMapSet,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// setPackage is the remedy every diagnostic names.
const setPackage = "github.com/wow-look-at-my/go-containers/set"

// mapSetWarned records the file:line of every struct-map warning this run
// emitted. Analyzers run concurrently across package variants, so the map is
// a sync.Map and resetMapSetWarnings clears it at the start of each vet run.
var mapSetWarned sync.Map

// resetMapSetWarnings forgets the warnings of the previous run, so a re-run
// after a fix reports its sites again.
func resetMapSetWarnings() { mapSetWarned.Clear() }

func runMapSet(pass *analysis.Pass) (any, error) {
	report := pass.Reportf
	if !isOrgModule(pass.Module) {
		report = func(pos token.Pos, format string, args ...any) { warnAt(pass, pos, format, args...) }
	}

	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			mt, ok := lit.Type.(*ast.MapType)
			if !ok || !isAllTrueBoolMap(pass, mt, lit) {
				return true
			}
			report(lit.Pos(), "map[…]bool with every value true is a set, not a map: use %s.Of(…) instead", setPackage)
			return true
		})
	}

	// The set package is the one place the advice cannot apply: Set[T] IS the
	// map[T]struct{}. Its eight storage sites would spend half the warnings
	// budget telling the remedy to use itself.
	if !isSetPackage(pass.Pkg) {
		for _, file := range pass.Files {
			warnEmptyStructMaps(pass, file)
		}
	}

	for _, c := range mapSetCandidates(pass) {
		if c.writes > 0 && !c.disqualified {
			report(c.pos, "map[…]bool is only ever used as a set: use %s.Set instead", setPackage)
		}
	}

	return []*ASTFixes(nil), nil
}

// warnEmptyStructMaps warns about a map[K]struct{}. That map already carries
// no value, so it is not the mistake this analyzer fails a build over: set.Set
// still gives it the membership operations, and which of the two to write is
// the author's call. The warning says so once per site and counts against the
// warnings budget; it never fails the run by itself.
func warnEmptyStructMaps(pass *analysis.Pass, file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		mt, ok := n.(*ast.MapType)
		if !ok || !isEmptyStructType(pass, mt.Value) {
			return true
		}
		warnAt(pass, mt.Pos(), "map[…]struct{} is a set: %s.Set carries the membership operations", setPackage)
		return true
	})
}

// warnAt emits one finding as a warning. go/packages loads a package up to
// four ways (plain, internal test, external test, test main) and every variant
// walks the same file, so a site spends one warning of the budget, not four.
func warnAt(pass *analysis.Pass, pos token.Pos, format string, args ...any) {
	p := pass.Fset.Position(pos)
	if _, dup := mapSetWarned.LoadOrStore(fmt.Sprintf("%s:%d", p.Filename, p.Line), true); dup {
		return
	}
	logger.WarnFile(p.Filename, "%s:%d: "+format, append([]any{p.Filename, p.Line}, args...)...)
}

// isOrgModule reports whether the module under analysis is org code, where the
// remedy is a first-party dependency. A driver that supplies no module info
// fails open to org, so the analysistest fixtures still expect diagnostics.
func isOrgModule(mod *analysis.Module) bool {
	if mod == nil || mod.Path == "" {
		return true
	}
	for _, prefix := range mapSetOrgPrefixes {
		if strings.HasPrefix(mod.Path, prefix) {
			return true
		}
	}
	return false
}

// mapSetOrgPrefixes are the module paths whose findings fail the build.
var mapSetOrgPrefixes = []string{
	"github.com/wow-look-at-my/",
	"github.com/PazerOP/",
}

// isSetPackage reports whether pkg is the set package itself, under its own
// path or its external test variant.
func isSetPackage(pkg *types.Package) bool {
	return pkg != nil && strings.TrimSuffix(pkg.Path(), "_test") == setPackage
}

// isEmptyStructType reports whether expr names a struct type with no fields.
func isEmptyStructType(pass *analysis.Pass, expr ast.Expr) bool {
	if t := pass.TypesInfo.TypeOf(expr); t != nil {
		st, ok := t.Underlying().(*types.Struct)
		return ok && st.NumFields() == 0
	}
	st, ok := expr.(*ast.StructType)
	return ok && st.Fields.NumFields() == 0
}

// isAllTrueBoolMap reports whether lit is a non-empty map[K]bool literal whose
// values are all the constant true.
func isAllTrueBoolMap(pass *analysis.Pass, mt *ast.MapType, lit *ast.CompositeLit) bool {
	if len(lit.Elts) == 0 || !isBoolType(pass, mt.Value) {
		return false
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok || !isTrueIdent(pass, kv.Value) {
			return false
		}
	}
	return true
}

// isBoolType reports whether expr names the predeclared bool type.
func isBoolType(pass *analysis.Pass, expr ast.Expr) bool {
	if t := pass.TypesInfo.TypeOf(expr); t != nil {
		b, ok := t.Underlying().(*types.Basic)
		return ok && b.Kind() == types.Bool
	}
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "bool"
}

// isTrueIdent reports whether expr is the predeclared true.
func isTrueIdent(pass *analysis.Pass, expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	if !ok || id.Name != "true" {
		return false
	}
	if obj := pass.TypesInfo.ObjectOf(id); obj != nil {
		return obj == types.Universe.Lookup("true")
	}
	return true
}

// mapSetCandidate is one map[K]bool variable and what the package does to it.
type mapSetCandidate struct {
	pos          token.Pos
	writes       int
	disqualified bool
}

// mapSetCandidates finds every map[K]bool variable the package creates empty,
// then classifies each of its uses.
func mapSetCandidates(pass *analysis.Pass) map[types.Object]*mapSetCandidate {
	candidates := make(map[types.Object]*mapSetCandidate)
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				if x.Tok != token.DEFINE || len(x.Lhs) != len(x.Rhs) {
					return true
				}
				for i, lhs := range x.Lhs {
					mapSetAddCandidate(pass, candidates, lhs, x.Rhs[i])
				}
			case *ast.ValueSpec:
				for i, name := range x.Names {
					var value ast.Expr
					if i < len(x.Values) {
						value = x.Values[i]
					}
					mapSetAddCandidate(pass, candidates, name, value)
				}
			}
			return true
		})
	}
	if len(candidates) == 0 {
		return candidates
	}
	for _, file := range pass.Files {
		mapSetUses(pass, file, candidates)
	}
	return candidates
}

// mapSetAddCandidate records name as a candidate when it is a map[K]bool that
// value creates empty. A non-empty literal is left to the literal rule, and a
// map that arrives from somewhere else says nothing about its own values.
func mapSetAddCandidate(pass *analysis.Pass, candidates map[types.Object]*mapSetCandidate, name, value ast.Expr) {
	id, ok := name.(*ast.Ident)
	if !ok || id.Name == "_" {
		return
	}
	obj := pass.TypesInfo.ObjectOf(id)
	if obj == nil || !isBoolValuedMap(obj.Type()) {
		return
	}
	switch v := value.(type) {
	case nil: // var x map[K]bool
	case *ast.CallExpr:
		fn, ok := v.Fun.(*ast.Ident)
		if !ok || fn.Name != "make" {
			return
		}
	case *ast.CompositeLit:
		if len(v.Elts) > 0 {
			return
		}
	default:
		return
	}
	candidates[obj] = &mapSetCandidate{pos: id.Pos()}
}

// isBoolValuedMap reports whether t is a map type with bool values.
func isBoolValuedMap(t types.Type) bool {
	m, ok := t.Underlying().(*types.Map)
	if !ok {
		return false
	}
	b, ok := m.Elem().Underlying().(*types.Basic)
	return ok && b.Kind() == types.Bool
}

// mapSetUses classifies every reference to a candidate. A use that could read
// a real boolean, or that hands the map to code this walk cannot see,
// disqualifies the candidate:
//
//   - v, ok := m[k] -- present-and-false is not the same as absent
//   - m[k] = <anything but true>, and any compound assignment
//   - for k, v := range m, where v is used
//   - the map as a value: an argument other than to len/delete/clear, a return,
//     a struct field, a channel send, &m
func mapSetUses(pass *analysis.Pass, file *ast.File, candidates map[types.Object]*mapSetCandidate) {
	var stack []ast.Node
	ast.Inspect(file, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		c := candidates[pass.TypesInfo.Uses[id]]
		if c == nil {
			return true
		}
		var parent, grandparent ast.Node
		if len(stack) >= 2 {
			parent = stack[len(stack)-2]
		}
		if len(stack) >= 3 {
			grandparent = stack[len(stack)-3]
		}
		classifyMapSetUse(pass, c, id, parent, grandparent)
		return true
	})
}

// classifyMapSetUse records one use against the candidate.
func classifyMapSetUse(pass *analysis.Pass, c *mapSetCandidate, id *ast.Ident, parent, grandparent ast.Node) {
	switch p := parent.(type) {
	case *ast.IndexExpr:
		if p.X != id {
			return // the map is the key of some other map
		}
		classifyMapSetIndex(pass, c, p, grandparent)
	case *ast.CallExpr:
		fn, ok := p.Fun.(*ast.Ident)
		if !ok || fn == id {
			c.disqualified = true
			return
		}
		// len, delete and clear read or empty the map without escaping it.
		if _, isBuiltin := pass.TypesInfo.ObjectOf(fn).(*types.Builtin); !isBuiltin {
			c.disqualified = true
			return
		}
		switch fn.Name {
		case "len", "delete", "clear":
		default:
			c.disqualified = true
		}
	case *ast.RangeStmt:
		if p.X != id {
			c.disqualified = true
			return
		}
		if p.Value != nil && !isBlankIdent(p.Value) {
			c.disqualified = true
		}
	default:
		c.disqualified = true
	}
}

// classifyMapSetIndex records an m[k] use, which is a write, a membership
// read, or the comma-ok form that reads a real boolean.
func classifyMapSetIndex(pass *analysis.Pass, c *mapSetCandidate, index *ast.IndexExpr, grandparent ast.Node) {
	switch g := grandparent.(type) {
	case *ast.AssignStmt:
		for i, lhs := range g.Lhs {
			if lhs != ast.Expr(index) {
				continue
			}
			if g.Tok != token.ASSIGN || len(g.Rhs) != len(g.Lhs) || !isTrueIdent(pass, g.Rhs[i]) {
				c.disqualified = true
				return
			}
			c.writes++
			return
		}
		// m[k] on the right: the two-name form reads present-and-false.
		if len(g.Lhs) == 2 && len(g.Rhs) == 1 {
			c.disqualified = true
		}
	case *ast.ValueSpec:
		if len(g.Names) == 2 && len(g.Values) == 1 {
			c.disqualified = true
		}
	}
}

// isBlankIdent reports whether expr is the blank identifier.
func isBlankIdent(expr ast.Expr) bool {
	id, ok := expr.(*ast.Ident)
	return ok && id.Name == "_"
}
