package vet

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/tools/go/analysis"
)

// DeadCodeAnalyzer detects unexported package-level symbols that are never
// referenced within the package: functions, types, constants, and variables.
var DeadCodeAnalyzer = &analysis.Analyzer{
	Name:       "deadcode",
	Doc:        "detects unexported package-level functions, types, constants, and variables that are never used",
	Run:        runDeadCode,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

func runDeadCode(pass *analysis.Pass) (any, error) {
	// Determine which files are generated so we can skip them entirely.
	generated := set.New[*ast.File]()
	for _, file := range pass.Files {
		if isGeneratedFile(file) {
			generated.Add(file)
		}
	}

	// Collect all unexported package-level definitions.
	type defInfo struct {
		pos  ast.Node // for position in Reportf
		name string
		kind string // "function", "type", "const", "var"
	}
	defined := make(map[types.Object]defInfo)

	for ident, obj := range pass.TypesInfo.Defs {
		if obj == nil {
			continue
		}
		// Skip generated files.
		file := fileForPos(pass, ident.Pos())
		if file != nil && generated.Contains(file) {
			continue
		}
		if shouldSkipDef(obj, ident.Name, pass) {
			continue
		}
		kind := objectKind(obj)
		if kind == "" {
			continue
		}
		defined[canonicalize(obj)] = defInfo{pos: ident, name: ident.Name, kind: kind}
	}

	// Remove everything that is referenced.
	for _, obj := range pass.TypesInfo.Uses {
		delete(defined, canonicalize(obj))
	}

	// Remove methods reachable through an interface.
	if len(defined) > 0 {
		ifaces := collectInterfaces(pass)
		named := collectNamedTypes(pass)
		ifaceMethods := collectUsedInterfaceMethods(pass)
		for obj := range defined {
			fn, ok := obj.(*types.Func)
			if !ok {
				continue
			}
			if isInterfaceMethod(fn, named, ifaces, pass.Pkg) || isCalledThroughInterface(fn, ifaceMethods) {
				delete(defined, obj)
			}
		}
	}

	// Report remaining dead code.
	for _, info := range defined {
		pass.Reportf(info.pos.Pos(), "%s %s is unused within this package", info.kind, info.name)
	}

	return []*ASTFixes(nil), nil
}

// canonicalize maps a generic method's instantiated *types.Func back to its Origin(), so calls resolve consistently.
func canonicalize(obj types.Object) types.Object {
	if fn, ok := obj.(*types.Func); ok {
		return fn.Origin()
	}
	return obj
}

// shouldSkipDef returns true for definitions that should never be flagged.
func shouldSkipDef(obj types.Object, name string, pass *analysis.Pass) bool {
	// Blank identifier.
	if name == "_" {
		return true
	}
	// Exported symbols may be used by other packages.
	if obj.Exported() {
		return true
	}
	// Must be at package scope (or a method).
	if obj.Parent() != pass.Pkg.Scope() {
		// Allow unexported methods — they'll be checked for interface impl.
		if _, ok := obj.(*types.Func); ok {
			sig := obj.Type().(*types.Signature)
			if sig.Recv() != nil {
				return false
			}
		}
		return true
	}
	// Special functions.
	if fn, ok := obj.(*types.Func); ok {
		if fn.Name() == "init" || fn.Name() == "main" {
			return true
		}
		if isTestEntryPoint(fn.Name()) {
			return true
		}
	}
	return false
}

// objectKind returns a human-readable kind string, or "" if the object type
// is not a kind we track.
func objectKind(obj types.Object) string {
	switch obj.(type) {
	case *types.Func:
		return "function"
	case *types.TypeName:
		return "type"
	case *types.Const:
		return "const"
	case *types.Var:
		// Only package-level vars, not parameters or struct fields.
		if obj.Parent() != nil {
			return "var"
		}
		return ""
	default:
		return ""
	}
}

// isTestEntryPoint returns true for function names that the test framework
// calls automatically (TestXxx, BenchmarkXxx, FuzzXxx, ExampleXxx).
func isTestEntryPoint(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			// Must have an uppercase letter or nothing after the prefix ("Test" alone is valid, "Testfoo" is not).
			rest := name[len(prefix):]
			if rest == "" || rest[0] < 'a' || rest[0] > 'z' {
				return true
			}
		}
	}
	return false
}

// isGeneratedFile checks for the standard "Code generated" marker per
// https://pkg.go.dev/cmd/go#hdr-Generate_Go_files_by_running_commands.
func isGeneratedFile(file *ast.File) bool {
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if strings.Contains(c.Text, "Code generated") && strings.Contains(c.Text, "DO NOT EDIT") {
				return true
			}
		}
	}
	return false
}

// fileForPos returns the *ast.File containing the given position.
func fileForPos(pass *analysis.Pass, pos token.Pos) *ast.File {
	for _, f := range pass.Files {
		if f.Pos() <= pos && pos <= f.End() {
			return f
		}
	}
	return nil
}

// collectInterfaces gathers all interface types visible from the package scope
// and all direct imports.
func collectInterfaces(pass *analysis.Pass) []*types.Interface {
	var ifaces []*types.Interface

	// From the current package scope.
	addInterfacesFromScope(pass.Pkg.Scope(), &ifaces)

	// From direct imports.
	for _, imp := range pass.Pkg.Imports() {
		addInterfacesFromScope(imp.Scope(), &ifaces)
	}

	return ifaces
}

// addInterfacesFromScope scans a scope for named interface types and appends them.
func addInterfacesFromScope(scope *types.Scope, ifaces *[]*types.Interface) {
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		tn, ok := obj.(*types.TypeName)
		if !ok {
			continue
		}
		iface, ok := tn.Type().Underlying().(*types.Interface)
		if !ok {
			continue
		}
		*ifaces = append(*ifaces, iface)
	}
}

// collectNamedTypes returns every non-interface named type declared in the
// package, at any scope. A generic type is instantiated with its own type
// parameters, the same form a method receiver has.
func collectNamedTypes(pass *analysis.Pass) []*types.Named {
	var named []*types.Named
	for _, obj := range pass.TypesInfo.Defs {
		tn, ok := obj.(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		n, ok := tn.Type().(*types.Named)
		if !ok || types.IsInterface(n) {
			continue
		}
		named = append(named, selfInstantiate(n))
	}
	return named
}

// selfInstantiate returns n instantiated with its own type parameters, or n
// itself when it is not generic.
func selfInstantiate(n *types.Named) *types.Named {
	tparams := n.TypeParams()
	if tparams.Len() == 0 {
		return n
	}
	args := make([]types.Type, tparams.Len())
	for i := range args {
		args[i] = tparams.At(i)
	}
	inst, err := types.Instantiate(nil, n, args, false)
	if err != nil {
		panic(err)
	}
	return inst.(*types.Named)
}

// collectUsedInterfaceMethods returns every interface method the package
// references: calls, method values, and method expressions.
func collectUsedInterfaceMethods(pass *analysis.Pass) []*types.Func {
	var methods []*types.Func
	for _, obj := range pass.TypesInfo.Uses {
		m, ok := obj.(*types.Func)
		if !ok {
			continue
		}
		recv := m.Type().(*types.Signature).Recv()
		if recv == nil || !types.IsInterface(recv.Type()) {
			continue
		}
		methods = append(methods, m)
	}
	return methods
}

// isInterfaceMethod returns true if some named type in the package has fn in
// its method set, declared on the type itself or promoted from an embedded
// field at any depth, and implements an interface that contains fn's name.
// Both T and *T are checked.
func isInterfaceMethod(fn *types.Func, named []*types.Named, ifaces []*types.Interface, pkg *types.Package) bool {
	if fn.Type().(*types.Signature).Recv() == nil {
		return false
	}
	for _, n := range named {
		if carriesInterfaceMethod(n, fn, ifaces, pkg) {
			return true
		}
	}
	return false
}

// carriesInterfaceMethod reports whether n or *n resolves fn's name to fn and
// implements an interface that has a method of that name.
func carriesInterfaceMethod(n *types.Named, fn *types.Func, ifaces []*types.Interface, pkg *types.Package) bool {
	for _, t := range []types.Type{n, types.NewPointer(n)} {
		obj, _, _ := types.LookupFieldOrMethod(t, false, pkg, fn.Name())
		m, ok := obj.(*types.Func)
		if !ok || m.Origin() != fn {
			continue
		}
		if implementsAnyWith(t, fn, ifaces) {
			return true
		}
	}
	return false
}

// implementsAnyWith reports whether t implements one of ifaces that has a
// method with fn's identifier.
func implementsAnyWith(t types.Type, fn *types.Func, ifaces []*types.Interface) bool {
	for _, iface := range ifaces {
		obj, _, _ := types.LookupFieldOrMethod(iface, false, fn.Pkg(), fn.Name())
		if obj == nil || obj.Id() != fn.Id() {
			continue
		}
		if types.Implements(t, iface) {
			return true
		}
	}
	return false
}

// isCalledThroughInterface reports whether the package uses an interface
// method with fn's identifier and an identical signature. Receivers are not
// part of the comparison.
func isCalledThroughInterface(fn *types.Func, ifaceMethods []*types.Func) bool {
	for _, m := range ifaceMethods {
		if m.Id() == fn.Id() && types.Identical(m.Type(), fn.Type()) {
			return true
		}
	}
	return false
}
