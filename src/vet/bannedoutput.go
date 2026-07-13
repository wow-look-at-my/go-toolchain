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
// The ban is scoped to the go-toolchain module itself: vetSemantic also runs
// this analyzer on every consumer project go-toolchain builds, and consumers
// have no src/logger to route output through — their fmt.Println is fine.
// Packages whose module path differs from bannedOutputModule are skipped.
//
// Exemptions (filename-based):
//   - any file under a src/logger/ directory (the logger must do real I/O)
//   - src/cmd/console.go (terminal animation UI — needs fine-grained newline control)
//   - *_test.go files (test code may print intentionally)
var BannedOutputAnalyzer = &analysis.Analyzer{
	Name:       "bannedoutput",
	Doc:        "bans direct writes to os.Stdout/os.Stderr; use the logger package instead",
	Run:        runBannedOutput,
	ResultType: reflect.TypeOf([]*ASTFixes{}),
}

// bannedOutputModule is the only module the ban applies to. The logger-routing
// convention is internal to go-toolchain; the diagnostic's remedy (the logger
// package) does not exist in consumer modules.
const bannedOutputModule = "github.com/wow-look-at-my/go-toolchain"

func runBannedOutput(pass *analysis.Pass) (any, error) {
	// Scope to the go-toolchain module. An empty module path means the driver
	// supplied no module info (e.g. analysistest's GOPATH-mode fixtures) — the
	// ban stays active there so the fixtures exercise the checks; the real
	// driver (vetSemantic) loads packages with packages.NeedModule, so consumer
	// modules carry their own path and are skipped.
	if pass.Module != nil && pass.Module.Path != "" && pass.Module.Path != bannedOutputModule {
		return []*ASTFixes(nil), nil
	}

	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()

		// Exempt: logger package internals.
		if strings.Contains(filename, "/src/logger/") {
			continue
		}
		// Exempt: console.go — terminal animation UI that needs fine-grained
		// newline control (same-line step progress and completion messages).
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
