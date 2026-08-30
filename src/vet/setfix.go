package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/ast/astutil"
)

// setPkgName is the local name the rewrite spells the set package as.
const setPkgName = "set"

// setKind says which container the variable holds today.
type setKind int

const (
	setFromMap setKind = iota
	setFromSlice
)

// fileEdit is a rewrite and the file it belongs to.
type fileEdit struct {
	file *ast.File
	fix  ASTFix
}

// setFixable reports whether this pass sees every use of obj. A local variable
// is used where it is declared. An exported package-level variable is reachable
// from another package, so it is never fixable here.
func setFixable(pass *analysis.Pass, obj types.Object) bool {
	if obj == nil || pass.Pkg == nil {
		return false
	}
	if obj.Parent() != pass.Pkg.Scope() {
		return true
	}
	if obj.Exported() || len(pass.Files) == 0 {
		return false
	}
	return passHoldsWholePackage(pass)
}

// passHoldsWholePackage reports whether pass.Files covers every file in the
// package's directory that can name an unexported package-level identifier.
// Tests load as their own variant: the plain variant lacks the in-package test
// files and must not rewrite, the internal-test variant holds them and may. An
// external test file reaches only exported names, so it never counts. Any other
// absent file -- excluded by this build configuration -- hides a use, and the
// rewrite that misses it does not compile.
func passHoldsWholePackage(pass *analysis.Pass) bool {
	held := set.New[string]()
	dir := filepath.Dir(pass.Fset.Position(pass.Files[0].Pos()).Filename)
	for _, f := range pass.Files {
		held.Add(filepath.Base(pass.Fset.Position(f.Pos()).Filename))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	external := pass.Pkg.Name() + "_test"
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || held.Contains(name) {
			continue
		}
		if declaredPackageName(filepath.Join(dir, name)) != external {
			return false
		}
	}
	return true
}

// declaredPackageName returns the package a file declares, or the empty string
// when it cannot be read.
func declaredPackageName(path string) string {
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.PackageClauseOnly)
	if err != nil || f.Name == nil {
		return ""
	}
	return f.Name.Name
}

// initializedVars maps each initializer expression to the variable it
// initializes, which is how a reported literal finds its name.
func initializedVars(pass *analysis.Pass) map[ast.Expr]types.Object {
	inits := make(map[ast.Expr]types.Object)
	record := func(name ast.Expr, value ast.Expr) {
		id, ok := name.(*ast.Ident)
		if !ok || id.Name == "_" {
			return
		}
		if obj := pass.TypesInfo.Defs[id]; obj != nil {
			inits[value] = obj
		}
	}
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.AssignStmt:
				if x.Tok == token.DEFINE && len(x.Lhs) == 1 && len(x.Rhs) == 1 {
					record(x.Lhs[0], x.Rhs[0])
				}
			case *ast.ValueSpec:
				if len(x.Names) == 1 && len(x.Values) == 1 {
					record(x.Names[0], x.Values[0])
				}
			}
			return true
		})
	}
	return inits
}

// setRewrites returns the edits that turn obj into a set, or nil when any use
// of obj has no set spelling. Half a rewrite does not compile, so an
// unspellable use blocks the whole variable. Depth: docs/VET.md
func setRewrites(pass *analysis.Pass, obj types.Object, kind setKind) []fileEdit {
	if obj == nil {
		return nil
	}
	var edits []fileEdit
	for _, file := range pass.Files {
		var stack []ast.Node
		blocked := false
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			stack = append(stack, n)
			id, ok := n.(*ast.Ident)
			if !ok || blocked {
				return true
			}
			def := pass.TypesInfo.Defs[id] == obj
			if !def && pass.TypesInfo.Uses[id] != obj {
				return true
			}
			fix, ok := rewriteSetSite(pass, id, def, kind, parentOf(stack, 2), parentOf(stack, 3))
			switch {
			case !ok:
				blocked = true
			case fix != nil:
				edits = append(edits, fileEdit{file: file, fix: *fix})
			}
			return true
		})
		if blocked {
			return nil
		}
	}
	return edits
}

// parentOf returns the node depth levels above the top of the stack.
func parentOf(stack []ast.Node, depth int) ast.Node {
	if len(stack) < depth {
		return nil
	}
	return stack[len(stack)-depth]
}

// rewriteSetSite returns the edit a mention of the variable needs. The
// ok result is false when the mention has no set spelling. A nil edit with
// a true result is a mention another edit already covers.
func rewriteSetSite(pass *analysis.Pass, id *ast.Ident, def bool, kind setKind, parent, grandparent ast.Node) (*ASTFix, bool) {
	if def {
		return rewriteSetDecl(id, kind, parent)
	}
	switch p := parent.(type) {
	case *ast.IndexExpr:
		return rewriteMapIndex(pass, id, p, grandparent)
	case *ast.CallExpr:
		return rewriteSetCallSite(pass, id, kind, p, grandparent)
	case *ast.RangeStmt:
		return rewriteSetRange(id, p)
	case *ast.AssignStmt:
		// The append that writes the variable back is rewritten from its
		// own argument, so the name on the left needs nothing here.
		if kind == setFromSlice && appendsBackTo(pass, p, id) {
			return nil, true
		}
	}
	return nil, false
}

// rewriteSetDecl rewrites the declaration into the matching set constructor.
func rewriteSetDecl(id *ast.Ident, kind setKind, parent ast.Node) (*ASTFix, bool) {
	switch p := parent.(type) {
	case *ast.AssignStmt:
		for i, lhs := range p.Lhs {
			if lhs != ast.Expr(id) {
				continue
			}
			if len(p.Rhs) != len(p.Lhs) {
				return nil, false
			}
			return constructorFix(p.Rhs[i], kind)
		}
	case *ast.ValueSpec:
		if len(p.Names) != 1 {
			return nil, false
		}
		if len(p.Values) == 1 {
			return constructorFix(p.Values[0], kind)
		}
		if p.Type == nil {
			return nil, false
		}
		elem := setElementType(p.Type, kind)
		if elem == nil {
			return nil, false
		}
		return &ASTFix{OldNode: p.Type, NewNodes: []ast.Node{setType(elem)}}, true
	}
	return nil, false
}

// constructorFix rewrites the expression that creates the container.
func constructorFix(value ast.Expr, kind setKind) (*ASTFix, bool) {
	switch v := value.(type) {
	case *ast.CallExpr: // make(...)
		if len(v.Args) == 0 {
			return nil, false
		}
		elem := setElementType(v.Args[0], kind)
		if elem == nil {
			return nil, false
		}
		return &ASTFix{OldNode: value, NewNodes: []ast.Node{setCall("New", elem)}}, true
	case *ast.CompositeLit:
		elem := setElementType(v.Type, kind)
		if elem == nil {
			return nil, false
		}
		if len(v.Elts) == 0 {
			return &ASTFix{OldNode: value, NewNodes: []ast.Node{setCall("New", elem)}}, true
		}
		elems, ok := literalElements(v, kind)
		if !ok {
			return nil, false
		}
		return &ASTFix{OldNode: value, NewNodes: []ast.Node{setCall("Of", elem, elems...)}}, true
	}
	return nil, false
}

// literalElements returns what the literal puts in the set: a map's keys, or a
// slice's own elements.
func literalElements(lit *ast.CompositeLit, kind setKind) ([]ast.Expr, bool) {
	elems := make([]ast.Expr, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		kv, keyed := elt.(*ast.KeyValueExpr)
		switch {
		case kind == setFromMap && keyed:
			elems = append(elems, kv.Key)
		case kind == setFromSlice && !keyed:
			elems = append(elems, elt)
		default:
			return nil, false
		}
	}
	return elems, true
}

// setElementType returns the type the set holds, read off the container's type
// expression. A slice with a length is a buffer, so it has none.
func setElementType(expr ast.Expr, kind setKind) ast.Expr {
	switch t := expr.(type) {
	case *ast.MapType:
		if kind == setFromMap {
			return t.Key
		}
	case *ast.ArrayType:
		if kind == setFromSlice && t.Len == nil {
			return t.Elt
		}
	}
	return nil
}

// rewriteMapIndex rewrites m[k], which either writes a member or reads it.
func rewriteMapIndex(pass *analysis.Pass, id *ast.Ident, index *ast.IndexExpr, grandparent ast.Node) (*ASTFix, bool) {
	if index.X != ast.Expr(id) {
		return nil, false
	}
	assign, ok := grandparent.(*ast.AssignStmt)
	if !ok {
		return &ASTFix{OldNode: index, NewNodes: []ast.Node{method(id, "Contains", index.Index)}}, true
	}
	for _, lhs := range assign.Lhs {
		if lhs != ast.Expr(index) {
			continue
		}
		if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 || !isTrueIdent(pass, assign.Rhs[0]) {
			return nil, false
		}
		return &ASTFix{OldNode: assign, NewNodes: []ast.Node{&ast.ExprStmt{X: method(id, "Add", index.Index)}}}, true
	}
	return &ASTFix{OldNode: index, NewNodes: []ast.Node{method(id, "Contains", index.Index)}}, true
}

// rewriteSetCallSite rewrites the calls that read or write the container.
func rewriteSetCallSite(pass *analysis.Pass, id *ast.Ident, kind setKind, call *ast.CallExpr, grandparent ast.Node) (*ASTFix, bool) {
	switch {
	case isBuiltinCall(pass, call, "len"):
		return &ASTFix{OldNode: call, NewNodes: []ast.Node{method(id, "Len")}}, true
	case isBuiltinCall(pass, call, "clear"):
		return &ASTFix{OldNode: call, NewNodes: []ast.Node{method(id, "Clear")}}, true
	case isBuiltinCall(pass, call, "delete") && len(call.Args) == 2:
		return &ASTFix{OldNode: call, NewNodes: []ast.Node{method(id, "Remove", call.Args[1])}}, true
	case kind == setFromSlice && isBuiltinCall(pass, call, "append"):
		return rewriteAppend(pass, id, call, grandparent)
	case kind == setFromSlice && isSlicesLookup(pass, call.Fun) && len(call.Args) == 2:
		sel, _ := call.Fun.(*ast.SelectorExpr)
		if sel.Sel.Name != "Contains" || call.Args[0] != ast.Expr(id) {
			return nil, false
		}
		return &ASTFix{OldNode: call, NewNodes: []ast.Node{method(id, "Contains", call.Args[1])}}, true
	}
	return nil, false
}

// rewriteAppend turns the write-back append into an insert. Only the append
// that lands in the same variable has an insert, since a set is what that variable
// becomes.
func rewriteAppend(pass *analysis.Pass, id *ast.Ident, call *ast.CallExpr, grandparent ast.Node) (*ASTFix, bool) {
	assign, ok := grandparent.(*ast.AssignStmt)
	if !ok || len(call.Args) < 2 || call.Args[0] != ast.Expr(id) {
		return nil, false
	}
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 || assign.Rhs[0] != ast.Expr(call) {
		return nil, false
	}
	lhs, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || pass.TypesInfo.Uses[lhs] != pass.TypesInfo.Uses[id] {
		return nil, false
	}
	name := "Add"
	if call.Ellipsis != token.NoPos || len(call.Args) > 2 {
		name = "AddRange"
	}
	added := method(id, name, call.Args[1:]...)
	added.Ellipsis = call.Ellipsis
	return &ASTFix{OldNode: assign, NewNodes: []ast.Node{&ast.ExprStmt{X: added}}}, true
}

// rewriteSetRange turns the range over the container into a range over the
// set's iterator. A slice loop also loses its index position.
func rewriteSetRange(id *ast.Ident, rng *ast.RangeStmt) (*ASTFix, bool) {
	if rng.X != ast.Expr(id) {
		return nil, false
	}
	over := &ast.RangeStmt{Tok: rng.Tok, X: method(id, "All"), Body: rng.Body}
	switch {
	case rng.Key == nil && rng.Value == nil: // for range m
	case rng.Value == nil: // for k := range m
		over.Key = rng.Key
	case isBlankIdent(rng.Key): // for _, v := range s
		over.Key = rng.Value
	default:
		return nil, false
	}
	if over.Key == nil {
		over.Tok = token.ILLEGAL
	}
	return &ASTFix{OldNode: rng, NewNodes: []ast.Node{over}}, true
}

// appendsBackTo reports whether assign writes an append to id back into it.
func appendsBackTo(pass *analysis.Pass, assign *ast.AssignStmt, id *ast.Ident) bool {
	if len(assign.Lhs) != 1 || len(assign.Rhs) != 1 || assign.Lhs[0] != ast.Expr(id) {
		return false
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok || !isBuiltinCall(pass, call, "append") || len(call.Args) == 0 {
		return false
	}
	arg, ok := call.Args[0].(*ast.Ident)
	return ok && pass.TypesInfo.Uses[arg] == pass.TypesInfo.Uses[id]
}

// setCall builds set.<name>[<elem>](args...). The type argument is explicit:
// inference off an untyped constant would pick a different element type.
func setCall(name string, elem ast.Expr, args ...ast.Expr) *ast.CallExpr {
	fun := &ast.IndexExpr{
		X:     &ast.SelectorExpr{X: ast.NewIdent(setPkgName), Sel: ast.NewIdent(name)},
		Index: elem,
	}
	return &ast.CallExpr{Fun: fun, Args: args}
}

// setType builds set.Set[<elem>].
func setType(elem ast.Expr) ast.Expr {
	return &ast.IndexExpr{
		X:     &ast.SelectorExpr{X: ast.NewIdent(setPkgName), Sel: ast.NewIdent("Set")},
		Index: elem,
	}
}

// method builds <name of id>.<call>(args...). The receiver is a fresh
// identifier: reusing the identifier being replaced puts a node inside its own
// replacement.
func method(id *ast.Ident, name string, args ...ast.Expr) *ast.CallExpr {
	return &ast.CallExpr{
		Fun:  &ast.SelectorExpr{X: ast.NewIdent(id.Name), Sel: ast.NewIdent(name)},
		Args: args,
	}
}

// setFixesByFile groups the edits per file and adds the set import to each
// file it touches. A file that already spells something else "set" keeps its
// diagnostic and loses its fix.
func setFixesByFile(pass *analysis.Pass, edits []fileEdit) []*ASTFixes {
	byFile := make(map[*ast.File][]ASTFix)
	for _, e := range edits {
		byFile[e.file] = append(byFile[e.file], e.fix)
	}
	var result []*ASTFixes
	for file, fixes := range byFile {
		if !setNameFree(file) {
			continue
		}
		astutil.AddImport(pass.Fset, file, setPackage)
		result = append(result, &ASTFixes{File: file, Fset: pass.Fset, Fixes: fixes})
	}
	return result
}

// setNameFree reports whether "set" names the set package in file, or nothing at all.
func setNameFree(file *ast.File) bool {
	for _, imp := range file.Imports {
		path := ""
		if imp.Path != nil {
			path = imp.Path.Value
		}
		isSetPath := path == `"`+setPackage+`"`
		if imp.Name == nil {
			// The package's own name is what an unnamed import binds.
			continue
		}
		if imp.Name.Name == setPkgName && !isSetPath {
			return false
		}
		if isSetPath && imp.Name.Name != setPkgName {
			return false
		}
	}
	return true
}
