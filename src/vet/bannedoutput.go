package vet

import (
	"go/ast"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// BannedOutputAnalyzer reports direct writes to os.Stdout / os.Stderr via
// fmt.Printf/Println/Print, fmt.Fprintf|Fprintln|Fprint(os.Stdout|Stderr, …),
// and log.Printf|Println|Print|Fatal*|Panic*.
//
// These are banned because all output must go through the logger package so
// that level filtering, GHA annotations, and colour support work uniformly.
//
// Exemptions (filename-based):
//   - any file under a src/logger/ directory (the logger must do real I/O)
//   - any file under a testdata/ directory (analyser test fixtures)
//   - *_test.go files (test code may print intentionally)
var BannedOutputAnalyzer = &analysis.Analyzer{
	Name:       "bannedoutput",
	Doc:        "bans direct writes to os.Stdout/os.Stderr; use the logger package instead",
	Run:        runBannedOutput,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

func runBannedOutput(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()

		// Exempt: logger package internals.
		if strings.Contains(filename, "/src/logger/") {
			continue
		}
		// Exempt: testdata directories (analyser fixture files).
		if strings.Contains(filename, "/testdata/") {
			continue
		}
		// Exempt: test files.
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}

			switch pkg.Name {
			case "fmt":
				checkFmtCall(pass, call, sel.Sel.Name)
			case "log":
				checkLogCall(pass, call, sel.Sel.Name)
			}
			return true
		})
	}

	return []*ASTFixes(nil), nil
}

// checkFmtCall reports banned fmt.* calls.
func checkFmtCall(pass *analysis.Pass, call *ast.CallExpr, fnName string) {
	switch fnName {
	case "Printf", "Println", "Print":
		pass.Reportf(call.Pos(), "banned: fmt.%s writes to stdout; use logger.Info / logger.Output instead", fnName)

	case "Fprintf", "Fprintln", "Fprint":
		// Only ban when the first argument is os.Stdout or os.Stderr.
		if len(call.Args) < 1 {
			return
		}
		if isOsStdioIdent(call.Args[0]) {
			pass.Reportf(call.Pos(), "banned: fmt.%s(os.Stdout|Stderr, ...) writes to stdio; use the logger package instead", fnName)
		}
	}
}

// checkLogCall reports banned log.* calls.
func checkLogCall(pass *analysis.Pass, call *ast.CallExpr, fnName string) {
	switch {
	case fnName == "Printf" || fnName == "Println" || fnName == "Print":
		pass.Reportf(call.Pos(), "banned: log.%s writes to stderr; use the logger package instead", fnName)
	case strings.HasPrefix(fnName, "Fatal"):
		pass.Reportf(call.Pos(), "banned: log.%s writes to stderr; use logger.Error + os.Exit instead", fnName)
	case strings.HasPrefix(fnName, "Panic"):
		pass.Reportf(call.Pos(), "banned: log.%s writes to stderr; use logger.Error + panic instead", fnName)
	}
}

// isOsStdioIdent returns true if expr is os.Stdout or os.Stderr.
func isOsStdioIdent(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return pkg.Name == "os" && (sel.Sel.Name == "Stdout" || sel.Sel.Name == "Stderr")
}
