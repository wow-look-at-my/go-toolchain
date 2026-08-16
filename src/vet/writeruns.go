package vet

import (
	"go/ast"
	"reflect"
	"sync"

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
var writeMethods = map[string]bool{
	"Write":       true,
	"WriteString": true,
	"WriteByte":   true,
	"WriteRune":   true,
}

// printfFuncs are the fmt functions whose first argument is the writer.
var printfFuncs = map[string]bool{
	"Fprint":   true,
	"Fprintf":  true,
	"Fprintln": true,
}

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
		call, writer, ok := writeCall(stmt)
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

// writeCall reports whether a statement is a write whose result is dropped,
// and names the writer it writes to. The name is the key a run groups by, so
// only a writer this can spell -- an identifier, or a chain of fields -- counts
// as a write.
func writeCall(stmt ast.Stmt) (*ast.CallExpr, string, bool) {
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
	if id, ok := sel.X.(*ast.Ident); ok && id.Name == "fmt" && printfFuncs[sel.Sel.Name] {
		if len(call.Args) == 0 {
			return nil, "", false
		}
		writer := writerName(call.Args[0])
		return call, writer, writer != ""
	}
	if !writeMethods[sel.Sel.Name] {
		return nil, "", false
	}
	writer := writerName(sel.X)
	return call, writer, writer != ""
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
