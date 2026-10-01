package vet

import (
	"go/ast"
	"go/token"
	"go/types"
	"reflect"
	"sync"

	"github.com/wow-look-at-my/go-containers/set"
	"golang.org/x/tools/go/analysis"
)

// WriteRunsAnalyzer flags a run of adjacent statements writing literal text to the
// same writer: render it with text/template instead. A computed value, or a
// hash writer, never joins a run (see writeCall). Depth: docs/VET.md
var WriteRunsAnalyzer = &analysis.Analyzer{
	Name:       "writeruns",
	Doc:        "reports a run of adjacent writes to one writer; render the text with text/template instead",
	Run:        runWriteRuns,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// writeRunFree is the number of adjacent writes a run spends before it warns.
const writeRunFree = 2

// writeRunWarned records each warning's file:line, so the package variants that walk the same file warn per site.
var writeRunWarned sync.Map

// resetWriteRunWarnings forgets the previous run's warnings, so a re-run after a fix reports its sites again.
func resetWriteRunWarnings() { writeRunWarned.Clear() }

// writeMethods are the writer methods that put text into the output.
var writeMethods = set.Of("Write", "WriteString", "WriteByte", "WriteRune")

// printfFuncs are the fmt functions whose leading argument is the writer.
var printfFuncs = set.Of("Fprint", "Fprintf", "Fprintln")

func runWriteRuns(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.BlockStmt:
				checkWriteRun(pass, node.List)
			case *ast.CaseClause:
				checkWriteRun(pass, node.Body)
			case *ast.CommClause:
				checkWriteRun(pass, node.Body)
			}
			return true
		})
	}
	return []*ASTFixes(nil), nil
}

// checkWriteRun walks a statement list and warns on each write of a run past
// the free allowance.
func checkWriteRun(pass *analysis.Pass, list []ast.Stmt) {
	target, length := "", 0
	for _, stmt := range list {
		call, writer, ok := writeCall(pass, stmt)
		if !ok || writer != target {
			target, length = writer, 0
		}
		if !ok {
			continue
		}
		length++
		if length <= writeRunFree {
			continue
		}
		warnAt(&writeRunWarned, pass, call.Pos(),
			"write %d in a row to %s: this text is a document, so render it with a text/template instead",
			length, writer)
	}
}

// writeCall reports whether a statement writes a piece of the document, and
// names the writer it writes to. Several things must hold. The result is
// dropped, so the statement is an expression. The written text is spelled in
// the source, so a call carrying no string or character literal writes a value
// this check cannot render. And the writer holds text: a hash reads its input
// as bytes, and a template has nothing to say about it.
func writeCall(pass *analysis.Pass, stmt ast.Stmt) (*ast.CallExpr, string, bool) {
	expr, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return nil, "", false
	}
	call, ok := expr.X.(*ast.CallExpr)
	if !ok {
		return nil, "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil, "", false
	}

	// fmt.Fprintf(w, ...) leads with the writer; a method names it as its receiver.
	target, text := sel.X, call.Args
	if id, isPkg := sel.X.(*ast.Ident); isPkg && writerFirstFunc(id.Name, sel.Sel.Name) {
		if len(call.Args) == 0 {
			return nil, "", false
		}
		target, text = call.Args[0], call.Args[1:]
	} else if !writeMethods.Contains(sel.Sel.Name) {
		return nil, "", false
	}

	writer := writerName(target)
	if writer == "" || !writesLiteral(text) || isHashWriter(pass, target) {
		return nil, "", false
	}
	return call, writer, true
}

// writerFirstFunc reports whether pkg.fn takes its writer as the leading
// argument.
func writerFirstFunc(pkg, fn string) bool {
	return (pkg == "fmt" && printfFuncs.Contains(fn)) || (pkg == "io" && fn == "WriteString")
}

// writesLiteral reports whether the text of a write is spelled in the source.
// A write of a computed value is not a document line, and no template renders it.
func writesLiteral(args []ast.Expr) bool {
	for _, arg := range args {
		lit, ok := arg.(*ast.BasicLit)
		if ok && (lit.Kind == token.STRING || lit.Kind == token.CHAR) {
			return true
		}
	}
	return false
}

// isHashWriter reports whether a writer digests its input instead of holding
// it. A hash's bytes are framing, not a document: templating them would change the digest.
func isHashWriter(pass *analysis.Pass, e ast.Expr) bool {
	if pass.TypesInfo == nil {
		return false
	}
	t := pass.TypesInfo.TypeOf(e)
	if t == nil {
		return false
	}
	sum, block := false, false
	for _, candidate := range []types.Type{t, types.NewPointer(t)} {
		ms := types.NewMethodSet(candidate)
		for i := range ms.Len() {
			switch ms.At(i).Obj().Name() {
			case "Sum":
				sum = true
			case "BlockSize":
				block = true
			}
		}
	}
	return sum && block
}

// writerName spells a writer expression, or "" for a shape that could name
// more than a single writer. Accepted shapes: w, s.buf, a.b.c.
func writerName(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		prefix := writerName(v.X)
		if prefix == "" {
			return ""
		}
		return prefix + "." + v.Sel.Name
	}
	return ""
}
