// typedAssertFunc picks the nil-family assertion from the operand's type.

package vet

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// typedAssertFunc returns the assertion the rewrite emits for an if that
// determineAssertion named assertFunc. The syntax alone cannot pick a nil
// check: NoError takes only an error. Nil on an interface passes for a typed
// nil that `x != nil` rejects. ok is false when no assertion keeps the
// meaning of the if. Such an if is left alone.
func typedAssertFunc(pass *analysis.Pass, ifStmt *ast.IfStmt, assertFunc string) (string, bool) {
	switch assertFunc {
	case "Nil", "NotNil", "NoError":
	default:
		return assertFunc, true
	}
	operand, wantNil, ok := nilOperand(ifStmt.Cond)
	if !ok {
		return "", false
	}
	return nilCheckFunc(pass.TypesInfo.TypeOf(operand), wantNil)
}

// It returns x and whether the assertion must require x to be nil. An if
// fails the test when its condition holds, so the assertion is the opposite
// of the condition.
func nilOperand(cond ast.Expr) (ast.Expr, bool, bool) {
	cond = ast.Unparen(cond)
	negated := false
	if u, ok := cond.(*ast.UnaryExpr); ok && u.Op == token.NOT {
		negated = true
		cond = ast.Unparen(u.X)
	}
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || (bin.Op != token.EQL && bin.Op != token.NEQ) {
		return nil, false, false
	}
	operand := bin.X
	switch {
	case isNil(bin.Y) && !isNil(bin.X):
	case isNil(bin.X) && !isNil(bin.Y):
		operand = bin.Y
	default:
		return nil, false, false
	}
	condIsNil := (bin.Op == token.EQL) != negated
	return operand, !condIsNil, true
}

// nilCheckFunc returns the assertion that checks a value of type typ against
// nil exactly as `== nil` does.
//   - An interface that implements error: NoError and Error compare the
//     interface itself to nil.
//   - A pointer, slice, map, channel, function or unsafe.Pointer: Nil and
//     NotNil see the same nil the comparison sees.
//   - Any other interface: Nil also passes for a typed nil inside it, so no
//     assertion matches.
func nilCheckFunc(typ types.Type, wantNil bool) (string, bool) {
	if typ == nil {
		return "", false
	}
	if types.IsInterface(typ) {
		if _, isParam := typ.(*types.TypeParam); isParam || !types.Implements(typ, errorInterface) {
			return "", false
		}
		if wantNil {
			return "NoError", true
		}
		return "Error", true
	}
	switch u := typ.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map, *types.Chan, *types.Signature:
	case *types.Basic:
		if u.Kind() != types.UnsafePointer {
			return "", false
		}
	default:
		return "", false
	}
	if wantNil {
		return "Nil", true
	}
	return "NotNil", true
}

// errorInterface is the predeclared error interface.
var errorInterface = types.Universe.Lookup("error").Type().Underlying().(*types.Interface)

// failureMessageArgs returns the failure call's arguments in the form testify
// takes as msgAndArgs. A t.Errorf format must be a plain string, since
// testify asserts the first of several arguments to string. A t.Error with
// more than one argument has no msgAndArgs form, so ok is false.
func failureMessageArgs(pass *analysis.Pass, call *ast.CallExpr) ([]ast.Expr, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) == 0 || call.Ellipsis != token.NoPos {
		return nil, false
	}
	switch sel.Sel.Name {
	case "Errorf", "Fatalf":
		if len(call.Args) > 1 && !isPlainString(pass.TypesInfo.TypeOf(call.Args[0])) {
			return nil, false
		}
	case "Error", "Fatal":
		if len(call.Args) != 1 {
			return nil, false
		}
	default:
		return nil, false
	}
	return call.Args, true
}

// isPlainString reports the predeclared string type, typed or untyped.
func isPlainString(typ types.Type) bool {
	b, ok := typ.(*types.Basic)
	return ok && (b.Kind() == types.String || b.Kind() == types.UntypedString)
}
