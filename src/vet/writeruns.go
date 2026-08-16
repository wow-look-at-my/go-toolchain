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

// WriteRunsAnalyzer reports a document that a run of adjacent write calls
// spells out one line at a time:
//
//	fmt.Fprintf(script, "  c=%s/$k\n", apeRunDir)
//	script.WriteString("  p=\"$c/${0##*/}\"\n")
//	script.WriteString("  if [ ! -x \"$p\" ]; then\n")
//
// The text is a document, so the escapes, the newlines and the writer name sit
// between the reader and the shape of the output. A text/template holds the
// same text as text, with the values named in it. A raw string constant does
// the same for a document with no values in it.
//
// The first two writes are free. Each write after them gets one warning, at
// the line of the write. A run is a group of adjacent statements that write to
// the SAME writer: any other statement between them ends the run.
//
// A write joins a run when it spells its text in the source, as a string or a
// character literal. A write of a computed value is a value the template names
// instead, and a writer that digests its input -- a hash -- holds no document
// at all, so neither one counts (see writeCall).
//
// This check never fails a build by itself. It warns, and a run long enough to
// exhaust the warnings budget fails the build through the budget.
//
// Depth: docs/VET.md
var WriteRunsAnalyzer = &analysis.Analyzer{
	Name:       "writeruns",
	Doc:        "reports a run of adjacent writes to one writer; render the text with text/template instead",
	Run:        runWriteRuns,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// writeRunFree is the number of adjacent writes a run spends before the
// warnings start. One write is a line. Two are a pair. The third makes the
// text a document.
const writeRunFree = 2

// writeRunWarned records the file:line of every warning this run emitted, so
// the four package variants that walk the same file spend one warning per
// site. resetWriteRunWarnings clears it at the start of each vet run.
var writeRunWarned sync.Map

// resetWriteRunWarnings forgets the warnings of the previous run, so a re-run
// after a fix reports its sites again.
func resetWriteRunWarnings() { writeRunWarned.Clear() }

// writeMethods are the writer methods that put text into the output. A call to
// one of them, with its result dropped, is a write.
var writeMethods = set.Of("Write", "WriteString", "WriteByte", "WriteRune")

// printfFuncs are the fmt functions whose first argument is the writer.
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

// checkWriteRun walks one statement list and warns on the third and every
// later write of each run.
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
// names the writer it writes to. Three things must hold. The result is
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

	// fmt.Fprintf(w, …) and io.WriteString(w, s) name the writer first; a
	// method names it as its receiver.
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

// writerFirstFunc reports whether pkg.fn takes its writer as the first
// argument.
func writerFirstFunc(pkg, fn string) bool {
	return (pkg == "fmt" && printfFuncs.Contains(fn)) || (pkg == "io" && fn == "WriteString")
}

// writesLiteral reports whether the text of a write is spelled in the source.
// A write of a computed value -- one byte of an escape, a marshalled payload --
// is not a line of a document, and no template renders it.
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
// it. The bytes a hash reads are framing, never a document: rendering them
// through a template would change the digest and help nobody.
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

// writerName spells a writer expression, and returns "" for one it cannot
// spell. A name that two different writers could share would group two runs
// into one, so the shapes it accepts are the ones that name one writer: w,
// s.buf, and a.b.c.
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
