package vet

import (
	"go/ast"
	"reflect"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// BannedOutputAnalyzer bans direct fmt/log writes to os.Stdout/os.Stderr in
// this module; use the logger package. Exempt: src/logger/, console.go, tests.
var BannedOutputAnalyzer = &analysis.Analyzer{
	Name:       "bannedoutput",
	Doc:        "bans direct writes to os.Stdout/os.Stderr; use the logger package instead",
	Run:        runBannedOutput,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// bannedOutputModule is the only module the ban applies to: the logger package doesn't exist elsewhere.
const bannedOutputModule = "github.com/wow-look-at-my/go-toolchain"

func runBannedOutput(pass *analysis.Pass) (any, error) {
	// An empty module path keeps the ban active for analysistest fixtures.
	if pass.Module != nil && pass.Module.Path != "" && pass.Module.Path != bannedOutputModule {
		return []*ASTFixes(nil), nil
	}

	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()

		// Exempt: logger package internals.
		if strings.Contains(filename, "/src/logger/") {
			continue
		}
		// Exempt: console.go's terminal UI needs fine-grained newline control.
		if strings.HasSuffix(filename, "/src/cmd/console.go") {
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
