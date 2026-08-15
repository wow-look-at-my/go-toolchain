package vet

import (
	"go/ast"
	"go/types"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// MapSetAnalyzer reports a map that carries no value, and so is a set written
// as a map. Two shapes are unambiguous and both are reported:
//
//   - map[K]struct{}, in any position — a type, a make call, a literal, a
//     field, a parameter. An empty struct value has nothing to say.
//   - a map[K]bool composite literal whose every value is the constant true.
//     A literal that writes one false is a lookup table and is not reported.
//
// The remedy is github.com/wow-look-at-my/go-containers/set. Its Set[T] holds
// the membership operations a map spells out by hand: Contains, ContainsAll,
// Union, Intersection, Difference, and the subset predicates.
//
// The check is scoped to org modules (see mapSetModulePrefixes). go-toolchain
// vets every project it builds, and a third-party consumer must not get a red
// build over a remedy that adds an org dependency to their module.
//
// The set package itself is exempt: its Set[T] is the map[T]struct{} every
// other package is told to reach for.
//
// An escape hatch exists for a map that must stay a map: write
// "// go-toolchain:allow-mapset <reason>" on the line, or on the line above.
// Depth: docs/VET.md
var MapSetAnalyzer = &analysis.Analyzer{
	Name:       "mapset",
	Doc:        "detects a map used as a set; use github.com/wow-look-at-my/go-containers/set instead",
	Run:        runMapSet,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// mapSetModulePrefixes are the module paths this check applies to.
var mapSetModulePrefixes = []string{
	"github.com/wow-look-at-my/",
	"github.com/PazerOP/",
}

// mapSetAllowMarker suppresses one report.
const mapSetAllowMarker = "go-toolchain:allow-mapset"

// setPackage is the remedy every diagnostic names.
const setPackage = "github.com/wow-look-at-my/go-containers/set"

func runMapSet(pass *analysis.Pass) (any, error) {
	if !mapSetInScope(pass.Module) {
		return []*ASTFixes(nil), nil
	}
	// The remedy cannot take its own advice: set.Set IS a map[T]struct{}.
	if pass.Pkg != nil && strings.TrimSuffix(pass.Pkg.Path(), "_test") == setPackage {
		return []*ASTFixes(nil), nil
	}

	for _, file := range pass.Files {
		allowed := mapSetAllowedLines(pass, file)
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.MapType:
				if !isEmptyStructType(pass, x.Value) {
					return true
				}
				if allowed[pass.Fset.Position(x.Pos()).Line] {
					return true
				}
				pass.Reportf(x.Pos(), "map[…]struct{} is a set, not a map: use %s.Set instead (or write %q with a reason)",
					setPackage, mapSetAllowMarker)
			case *ast.CompositeLit:
				mt, ok := x.Type.(*ast.MapType)
				if !ok || !isAllTrueBoolMap(pass, mt, x) {
					return true
				}
				if allowed[pass.Fset.Position(x.Pos()).Line] {
					return true
				}
				pass.Reportf(x.Pos(), "map[…]bool with every value true is a set, not a map: use %s.Of(…) instead (or write %q with a reason)",
					setPackage, mapSetAllowMarker)
			}
			return true
		})
	}

	return []*ASTFixes(nil), nil
}

// mapSetInScope reports whether the module under analysis is org code. A
// driver that supplies no module info fails open to checked, so the
// analysistest fixtures still run the check.
func mapSetInScope(mod *analysis.Module) bool {
	if mod == nil || mod.Path == "" {
		return true
	}
	for _, prefix := range mapSetModulePrefixes {
		if strings.HasPrefix(mod.Path, prefix) {
			return true
		}
	}
	return false
}

// mapSetAllowedLines collects the lines an allow marker suppresses: the line
// the marker sits on, and the line below it.
func mapSetAllowedLines(pass *analysis.Pass, file *ast.File) map[int]bool {
	allowed := make(map[int]bool)
	for _, group := range file.Comments {
		for _, c := range group.List {
			if !strings.Contains(c.Text, mapSetAllowMarker) {
				continue
			}
			line := pass.Fset.Position(c.End()).Line
			allowed[line] = true
			allowed[line+1] = true
		}
	}
	return allowed
}

// isEmptyStructType reports whether expr names a struct type with no fields.
func isEmptyStructType(pass *analysis.Pass, expr ast.Expr) bool {
	t := pass.TypesInfo.TypeOf(expr)
	if t == nil {
		// The fixtures under testdata load without full type info.
		st, ok := expr.(*ast.StructType)
		return ok && st.Fields.NumFields() == 0
	}
	st, ok := t.Underlying().(*types.Struct)
	return ok && st.NumFields() == 0
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
