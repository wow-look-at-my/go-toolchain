package vet

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strconv"
	"sync"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/tools/go/analysis"
)

// JSONInterpAnalyzer reports a JSON document built out of string pieces: a
// format string, a concatenation, or a template. Org modules FAIL; others
// WARN. docs/VET.md
var JSONInterpAnalyzer = &analysis.Analyzer{
	Name:       "jsoninterp",
	Doc:        "detects JSON built by string interpolation or concatenation; marshal it with encoding/json instead",
	Run:        runJSONInterp,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// jsonPackage is the remedy every diagnostic names.
const jsonPackage = "encoding/json"

// jsonBreaks names what a value carries that the document cannot survive.
const jsonBreaks = "a quote, a backslash or a newline in a value breaks the document"

// jsonInterpWarned records file:line of every warning; the package variants that walk one file warn once per site.
var jsonInterpWarned sync.Map

// resetJSONInterpWarnings forgets prior warnings, so a re-run after a fix reports its sites again.
func resetJSONInterpWarnings() { jsonInterpWarned.Clear() }

// jsonFormatFuncs are the fmt functions that take a format string, and where
// that string sits in the argument list.
var jsonFormatFuncs = map[string]int{
	"Appendf": 1,
	"Errorf":  0,
	"Fprintf": 1,
	"Printf":  0,
	"Sprintf": 0,
}

// templatePackages render text with no JSON escaping of any kind.
var templatePackages = set.Of("text/template", "html/template")

func runJSONInterp(pass *analysis.Pass) (any, error) {
	report := pass.Reportf
	if !isOrgModule(pass.Module) {
		report = func(pos token.Pos, format string, args ...any) {
			warnAt(&jsonInterpWarned, pass, pos, format, args...)
		}
	}

	for _, file := range pass.Files {
		// One document reports once, so the operands of a reported concatenation are skipped.
		inner := set.New[*ast.BinaryExpr]()
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CallExpr:
				checkJSONFormat(pass, node, report)
				checkJSONTemplate(pass, node, report)
			case *ast.BinaryExpr:
				checkJSONConcat(pass, node, inner, report)
			}
			return true
		})
	}
	return []*ASTFixes(nil), nil
}

// checkJSONFormat reports a JSON document rendered by a format string. The
// verbs are what a value reaches the document through, and fmt escapes none of
// them: %q writes Go quoting, which is not JSON.
func checkJSONFormat(pass *analysis.Pass, call *ast.CallExpr, report func(token.Pos, string, ...any)) {
	idx, ok := formatArgIndex(pass, call)
	if !ok || idx >= len(call.Args) {
		return
	}
	format, ok := stringLiteral(call.Args[idx])
	if !ok {
		return
	}
	text, verbs := normalizeVerbs(format)
	if verbs == 0 || !isJSONDocument(text) {
		return
	}
	report(call.Pos(), "JSON built by formatting: %s; marshal it with %s instead", jsonBreaks, jsonPackage)
}

// checkJSONConcat reports a JSON document joined from pieces. The text checked
// is every literal piece of the whole concatenation, so a fragment that is
// meaningless alone is read as part of the document it belongs to.
func checkJSONConcat(pass *analysis.Pass, expr *ast.BinaryExpr, inner set.Set[*ast.BinaryExpr], report func(token.Pos, string, ...any)) {
	if expr.Op != token.ADD || inner.Contains(expr) {
		return
	}
	markConcatOperands(expr, inner)
	if isConstantExpr(pass, expr) {
		return
	}
	text, interpolated := concatText(expr)
	if !interpolated || !isJSONDocument(text) {
		return
	}
	report(expr.Pos(), "JSON built by concatenation: %s; marshal it with %s instead", jsonBreaks, jsonPackage)
}

// checkJSONTemplate reports a JSON document rendered by a template. No
// template package has a JSON context: text/template escapes nothing, and
// html/template escapes for HTML, which is a different document.
func checkJSONTemplate(pass *analysis.Pass, call *ast.CallExpr, report func(token.Pos, string, ...any)) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Parse" || len(call.Args) != 1 {
		return
	}
	if !isTemplateReceiver(pass, sel.X) {
		return
	}
	text, ok := stringLiteral(call.Args[0])
	if !ok {
		return
	}
	rendered, actions := normalizeActions(text)
	if actions == 0 || !isJSONDocument(rendered) {
		return
	}
	report(call.Pos(), "a JSON template: no template package has a JSON context, so %s; marshal it with %s instead",
		jsonBreaks, jsonPackage)
}

// markConcatOperands records the operands of a reported concatenation, so the
// walk does not report the same document again from inside it.
func markConcatOperands(expr *ast.BinaryExpr, inner set.Set[*ast.BinaryExpr]) {
	for _, side := range []ast.Expr{expr.X, expr.Y} {
		if nested, ok := side.(*ast.BinaryExpr); ok && nested.Op == token.ADD {
			inner.Add(nested)
			markConcatOperands(nested, inner)
		}
	}
}

// concatText spells the concatenation: each literal piece as written, every
// other operand as a hole. It reports whether any operand is a value.
func concatText(expr ast.Expr) (string, bool) {
	if add, ok := expr.(*ast.BinaryExpr); ok && add.Op == token.ADD {
		left, leftValue := concatText(add.X)
		right, rightValue := concatText(add.Y)
		return left + right, leftValue || rightValue
	}
	if lit, ok := stringLiteral(expr); ok {
		return lit, false
	}
	return string(jsonHole), true
}

// isConstantExpr reports whether the type checker folded expr to a constant. A
// constant document holds no value that can break it.
func isConstantExpr(pass *analysis.Pass, expr ast.Expr) bool {
	if pass.TypesInfo == nil {
		return false
	}
	return pass.TypesInfo.Types[expr].Value != nil
}

// formatArgIndex reports where a call's format string sits, for the fmt
// functions that take one.
func formatArgIndex(pass *analysis.Pass, call *ast.CallExpr) (int, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return 0, false
	}
	idx, known := jsonFormatFuncs[sel.Sel.Name]
	if !known || !isPackageNamed(pass, sel, "fmt") {
		return 0, false
	}
	return idx, true
}

// isPackageNamed reports whether a selector reads from the named package. The
// type checker answers when it resolved the import; a fixture that leaves the
// import unresolved is read off the identifier instead.
func isPackageNamed(pass *analysis.Pass, sel *ast.SelectorExpr, path string) bool {
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	if pass.TypesInfo != nil {
		if pkg, ok := pass.TypesInfo.Uses[id].(*types.PkgName); ok {
			return pkg.Imported().Path() == path
		}
	}
	return id.Name == path
}

// isTemplateReceiver reports whether a Parse call parses a template. The
// receiver is a *template.Template, however many calls built it; an unresolved
// fixture is read off the identifier the expression roots at.
func isTemplateReceiver(pass *analysis.Pass, recv ast.Expr) bool {
	if pass.TypesInfo != nil {
		if named := namedType(pass.TypesInfo.TypeOf(recv)); named != nil && named.Obj().Pkg() != nil {
			return templatePackages.Contains(named.Obj().Pkg().Path())
		}
	}
	return rootIdent(recv) == "template"
}

// namedType unwraps a pointer to the named type under it, or nil.
func namedType(t types.Type) *types.Named {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, _ := t.(*types.Named)
	return named
}

// rootIdent names the identifier an expression reads from, or "".
func rootIdent(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return rootIdent(e.X)
	case *ast.CallExpr:
		return rootIdent(e.Fun)
	}
	return ""
}

// stringLiteral reads the text a string literal spells, in either quoting.
func stringLiteral(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	text, err := strconv.Unquote(lit.Value)
	return text, err == nil
}
