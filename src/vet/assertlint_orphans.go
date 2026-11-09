// orphanedVars finds the variables a rewrite leaves declared and not used.

package vet

import (
	"go/ast"
	"go/token"
	"go/types"

	"github.com/wow-look-at-my/go-containers/set"
)

// orphanedVars returns the local variables that deleting the dropped nodes
// leaves declared and not used. A failure message is often the only reader of
// a loop index, and the compiler rejects the variable once it is gone. A read
// that an earlier fix in this file already deleted counts as gone too.
func (st *assertFixState) orphanedVars(ifStmt *ast.IfStmt, dropped []ast.Node) []*types.Var {
	seen := set.New[*types.Var]()
	var orphans []*types.Var
	for _, d := range dropped {
		ast.Inspect(d, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			v, ok := st.pass.TypesInfo.Uses[id].(*types.Var)
			if !ok || v.IsField() || v.Parent() == nil || v.Parent() == st.pass.Pkg.Scope() {
				return true
			}
			// A name the if itself declares leaves along with it (the NoError shape).
			if v.Pos() >= ifStmt.Pos() && v.Pos() < ifStmt.End() {
				return true
			}
			if st.unusedExempt.Contains(v.Pos()) || st.blankedVars.Contains(v) || !seen.Add(v) {
				return true
			}
			for _, u := range st.uses[v] {
				if !st.writes.Contains(u) && !st.inDropped(dropped, u) {
					return true
				}
			}
			orphans = append(orphans, v)
			return true
		})
	}
	return orphans
}

// collapsesNoErrorInit reports the `if err := X; err != nil` shape whose
// rewrite is `NoError(t, X)`. The init declares the variable the condition
// checks, X has that single result, and nothing the rewrite keeps reads the
// variable. Any other init is hoisted instead.
func (st *assertFixState) collapsesNoErrorInit(ifStmt *ast.IfStmt, assertFunc string, dropped []ast.Node) bool {
	if assertFunc != "NoError" {
		return false
	}
	assign, ok := ifStmt.Init.(*ast.AssignStmt)
	if !ok || assign.Tok != token.DEFINE || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
		return false
	}
	lhs, ok := assign.Lhs[0].(*ast.Ident)
	if !ok {
		return false
	}
	operand, _, ok := nilOperand(ifStmt.Cond)
	if !ok {
		return false
	}
	checked, ok := ast.Unparen(operand).(*ast.Ident)
	obj := st.pass.TypesInfo.Defs[lhs]
	if !ok || obj == nil || st.pass.TypesInfo.Uses[checked] != obj {
		return false
	}
	for _, u := range st.uses[obj] {
		if u == checked || nodesContain(dropped, u.Pos()) {
			continue
		}
		return false
	}
	return true
}

// nodesContain reports whether pos lies inside one of nodes.
func nodesContain(nodes []ast.Node, pos token.Pos) bool {
	for _, n := range nodes {
		if pos >= n.Pos() && pos < n.End() {
			return true
		}
	}
	return false
}

// inDropped reports whether id lies in dropped or in a node an earlier fix deleted.
func (st *assertFixState) inDropped(dropped []ast.Node, id *ast.Ident) bool {
	return nodesContain(dropped, id.Pos()) || nodesContain(st.dropped, id.Pos())
}

// messageCanStay reports whether the failure call's arguments can become the
// assertion's msgAndArgs. Each argument must be a name or a literal. An
// assertion evaluates its arguments on the passing path too, where a field
// read can dereference nil. A call can do work the test never did. Keeping
// the message must leave no variable unused.
func (st *assertFixState) messageCanStay(ifStmt *ast.IfStmt, errCall *ast.CallExpr, dropped []ast.Node) bool {
	args, ok := failureMessageArgs(st.pass, errCall)
	if !ok {
		return false
	}
	for _, a := range args {
		switch ast.Unparen(a).(type) {
		case *ast.Ident, *ast.BasicLit:
		default:
			return false
		}
	}
	return len(st.orphanedVars(ifStmt, append(dropped, errCall.Fun))) == 0
}

// allBlankable reports whether every orphan is a variable a `:=` range clause
// declares, read and written only inside dropped. Such a variable becomes _.
func (st *assertFixState) allBlankable(orphans []*types.Var, dropped []ast.Node) bool {
	for _, v := range orphans {
		if st.rangeDecls[v.Pos()] == nil {
			return false
		}
		for _, u := range st.uses[v] {
			if !st.inDropped(dropped, u) {
				return false
			}
		}
	}
	return true
}

// blankRangeVar renames the range variable v to _ when the file is printed.
// A clause left with only blanks becomes `for range`.
func (st *assertFixState) blankRangeVar(v *types.Var) {
	if !st.blankedVars.Add(v) {
		return
	}
	rs := st.rangeDecls[v.Pos()]
	for _, e := range []ast.Expr{rs.Key, rs.Value} {
		id, ok := e.(*ast.Ident)
		if !ok || id.Pos() != v.Pos() {
			continue
		}
		st.blanks = append(st.blanks, func() {
			id.Name = "_"
			dropBlankRangeVars(rs)
		})
	}
}
