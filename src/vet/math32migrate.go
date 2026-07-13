package vet

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	ansi "github.com/wow-look-at-my/ansi-writer"
	"golang.org/x/tools/go/ast/astutil"
)

const math32ImportPath = "github.com/chewxy/math32"

// math32Funcs lists math package functions/constants that have float32
// equivalents in github.com/chewxy/math32.
var math32Funcs = set.Of(
	// Functions
	"Abs", "Acos", "Acosh", "Asin", "Asinh",
	"Atan", "Atan2", "Atanh",
	"Cbrt", "Ceil", "Copysign", "Cos", "Cosh",
	"Dim",
	"Erf", "Erfc", "Exp", "Exp2", "Expm1",
	"Floor", "FMA",
	"Gamma",
	"Hypot",
	"Inf", "IsInf", "IsNaN",
	"Log", "Log10", "Log1p", "Log2",
	"Max", "Min", "Mod",
	"NaN",
	"Pow", "Pow10",
	"Remainder", "Round", "RoundToEven",
	"Signbit", "Sin", "Sincos", "Sinh", "Sqrt",
	"Tan", "Tanh", "Trunc",
	// Constants
	"E", "Pi", "Phi",
	"Sqrt2", "SqrtE", "SqrtPi", "SqrtPhi",
	"Ln2", "Log2E", "Ln10", "Log10E",
	"MaxFloat32", "SmallestNonzeroFloat32",
)

// mathKeepFuncs are math functions that already handle float32/uint32
// and should NOT be migrated to math32.
var mathKeepFuncs = set.Of(
	"Float32bits", "Float32frombits",
	"Float64bits", "Float64frombits",
)

var (
	// "cannot use X (variable of type float32) as float64 value in argument to math.Xxx"
	reArgToMath = regexp.MustCompile(`^(.+\.go):\d+:\d+: cannot use .+ as float64 value in argument to math\.(\w+)`)

	// "undefined: math32.xxx (but have Xxx)"
	reUndefinedMath32Func = regexp.MustCompile(`^(.+\.go):\d+:\d+: undefined: math32\.(\w+) \(but have (\w+)\)`)

	// "undefined: math32" (missing import entirely)
	reUndefinedMath32Pkg = regexp.MustCompile(`^(.+\.go):\d+:\d+: undefined: math32$`)

	// "use of internal package .../math32 not allowed"
	reInternalMath32 = regexp.MustCompile(`^(.+\.go):\d+:\d+: use of internal package .+/math32 not allowed`)
)

type math32FileFix struct {
	rewriteMathFuncs  set.Set[string] // math.Xxx → math32.Xxx
	caseFixes         map[string]string // math32.wrong → correctName
	caseFixPkg        map[string]string // math32.wrong → target package
	needsImport       bool
	fixInternalImport bool
}

// MigrateMath32 parses compiler load errors and fixes float32/float64
// mismatches involving the math package by rewriting calls to use
// github.com/chewxy/math32. Returns true if any files were modified.
func MigrateMath32(loadErrors []string) (bool, error) {
	fileFixes := make(map[string]*math32FileFix)

	getFix := func(filename string) *math32FileFix {
		f, ok := fileFixes[filename]
		if !ok {
			f = &math32FileFix{
				caseFixes:  make(map[string]string),
				caseFixPkg: make(map[string]string),
			}
			fileFixes[filename] = f
		}
		return f
	}

	for _, errStr := range loadErrors {
		if m := reArgToMath.FindStringSubmatch(errStr); m != nil {
			funcName := m[2]
			if math32Funcs.Contains(funcName) {
				fix := getFix(m[1])
				fix.rewriteMathFuncs.Add(funcName)
			}
			continue
		}

		if m := reUndefinedMath32Func.FindStringSubmatch(errStr); m != nil {
			filename, wrongName, correctName := m[1], m[2], m[3]
			fix := getFix(filename)
			fix.caseFixes[wrongName] = correctName
			if mathKeepFuncs.Contains(correctName) {
				fix.caseFixPkg[wrongName] = "math"
			} else {
				fix.caseFixPkg[wrongName] = "math32"
			}
			fix.needsImport = true
			continue
		}

		if m := reUndefinedMath32Pkg.FindStringSubmatch(errStr); m != nil {
			getFix(m[1]).needsImport = true
			continue
		}

		if m := reInternalMath32.FindStringSubmatch(errStr); m != nil {
			getFix(m[1]).fixInternalImport = true
			continue
		}
	}

	if len(fileFixes) == 0 {
		return false, nil
	}

	var anyFixed bool
	for filename, fix := range fileFixes {
		fixed, err := applyMath32Fix(filename, fix)
		if err != nil {
			return anyFixed, err
		}
		if fixed {
			anyFixed = true
		}
	}

	if anyFixed {
		cmd := exec.Command("go", "mod", "tidy")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return anyFixed, err
		}
	}

	return anyFixed, nil
}

func applyMath32Fix(filename string, fix *math32FileFix) (bool, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
	if err != nil {
		return false, err
	}

	var modified bool
	needsMathImport := false
	needsMath32Import := false

	// Fix internal math32 import → chewxy math32
	if fix.fixInternalImport {
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if strings.HasSuffix(path, "/math32") && path != math32ImportPath {
				old := path
				imp.Path.Value = `"` + math32ImportPath + `"`
				modified = true
				printMath32Fix(filename, old, math32ImportPath)
			}
		}
	}

	// Rewrite math.Xxx → math32.Xxx
	if !fix.rewriteMathFuncs.IsEmpty() {
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "math" {
				return true
			}
			if fix.rewriteMathFuncs.Contains(sel.Sel.Name) {
				printMath32Fix(filename, "math."+sel.Sel.Name, "math32."+sel.Sel.Name)
				ident.Name = "math32"
				modified = true
				needsMath32Import = true
			}
			return true
		})
	}

	// Fix math32.wrongCase → correct package and case
	if len(fix.caseFixes) > 0 {
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok || ident.Name != "math32" {
				return true
			}
			correctName, hasFix := fix.caseFixes[sel.Sel.Name]
			if !hasFix {
				return true
			}
			targetPkg := fix.caseFixPkg[sel.Sel.Name]
			old := "math32." + sel.Sel.Name
			sel.Sel.Name = correctName
			ident.Name = targetPkg
			printMath32Fix(filename, old, targetPkg+"."+correctName)
			modified = true
			if targetPkg == "math" {
				needsMathImport = true
			} else {
				needsMath32Import = true
			}
			return true
		})
	}

	// Update imports
	if modified || fix.needsImport {
		if needsMath32Import || fix.needsImport {
			astutil.AddImport(fset, f, math32ImportPath)
			modified = true
		}
		if needsMathImport {
			astutil.AddImport(fset, f, "math")
		}
		if !isMathPkgUsed(f) {
			astutil.DeleteImport(fset, f, "math")
		}
	}

	if !modified {
		return false, nil
	}

	out, err := os.Create(filename)
	if err != nil {
		return false, err
	}
	defer out.Close()

	return true, printer.Fprint(out, fset, f)
}

// isMathPkgUsed checks if "math" is still referenced as a package selector.
func isMathPkgUsed(f *ast.File) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if found {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if ok && ident.Name == "math" {
			found = true
		}
		return !found
	})
	return found
}

func printMath32Fix(filename, old, replacement string) {
	yellow := ansi.Concat(ansi.Yellow.FG, "fixed:", ansi.Reset)
	grey := ansi.Concat(ansi.BrightBlack.FG, filename, ansi.Reset)
	red := ansi.Concat(ansi.Red.FG, old, ansi.Reset)
	green := ansi.Concat(ansi.Green.FG, replacement, ansi.Reset)
	println(yellow, grey, red, "→", green)
}
