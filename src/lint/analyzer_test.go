package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
	"golang.org/x/tools/go/analysis"
)

func TestAnalyzerRun_DetectsDuplicates(t *testing.T) {
	// Test the analysis.Pass-based run function with duplicate code
	src := `package p

func handleUser(name string) {
	result := validate(name)
	if result != nil {
		log("failed for user")
		return
	}
	data := transform(name)
	if data == nil {
		log("transform failed")
		return
	}
	save(data)
	log("saved user")
}

func handleOrder(id string) {
	result := validate(id)
	if result != nil {
		log("failed for order")
		return
	}
	data := transform(id)
	if data == nil {
		log("transform failed")
		return
	}
	save(data)
	log("saved order")
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	require.NoError(t, err)

	var diagnostics []analysis.Diagnostic
	pass := &analysis.Pass{
		Analyzer: Analyzer,
		Fset:     fset,
		Files:    []*ast.File{f},
		Report: func(d analysis.Diagnostic) {
			diagnostics = append(diagnostics, d)
		},
	}

	result, err := run(pass)
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.NotEmpty(t, diagnostics, "should report diagnostics for near-duplicate functions")
	assert.Contains(t, diagnostics[0].Message, "near-duplicate code")
	assert.NotEmpty(t, diagnostics[0].SuggestedFixes)
}

func TestAnalyzerRun_NoDuplicates(t *testing.T) {
	src := `package p

func add(a, b int) int {
	return a + b
}

func sub(a, b int) int {
	result := a - b
	if result < 0 {
		result = -result
	}
	return result
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	require.NoError(t, err)

	var diagnostics []analysis.Diagnostic
	pass := &analysis.Pass{
		Analyzer: Analyzer,
		Fset:     fset,
		Files:    []*ast.File{f},
		Report: func(d analysis.Diagnostic) {
			diagnostics = append(diagnostics, d)
		},
	}

	result, err := run(pass)
	require.NoError(t, err)
	assert.Nil(t, result)
	assert.Empty(t, diagnostics, "should not report diagnostics for different functions")
}

func TestRunOnFiles_DetectsNearDuplicates(t *testing.T) {
	// Two functions with identical structure, different variable names
	src := `package p

func processUser(name string) error {
	result := validate(name)
	if result != nil {
		log("validation failed for user")
		return result
	}
	data := transform(name)
	if data == nil {
		log("transform failed for user")
		return errTransform
	}
	save(data)
	log("saved user")
	return nil
}

func processOrder(id string) error {
	result := validate(id)
	if result != nil {
		log("validation failed for order")
		return result
	}
	data := transform(id)
	if data == nil {
		log("transform failed for order")
		return errTransform
	}
	save(data)
	log("saved order")
	return nil
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	require.NoError(t, err)

	files := map[string]*ast.File{"test.go": f}
	reports := RunOnFiles(files, fset, 0.85, 5)

	// Should detect the function-level pair plus inner-block pairs
	require.NotEmpty(t, reports, "should detect duplicate pairs")

	// Find the function-level pair among the results
	var foundFuncPair bool
	for _, r := range reports {
		names := []string{r.FuncA, r.FuncB}
		if r.FuncA == "processUser" && r.FuncB == "processOrder" ||
			r.FuncA == "processOrder" && r.FuncB == "processUser" {
			foundFuncPair = true
			assert.Greater(t, r.Similarity, 0.85)
			assert.Contains(t, names, "processUser")
			assert.Contains(t, names, "processOrder")
		}
	}
	assert.True(t, foundFuncPair, "should detect the function-level duplicate pair")
}

func TestRunOnFiles_NoDuplicates(t *testing.T) {
	src := `package p

func add(a, b int) int {
	return a + b
}

func multiply(a, b int) int {
	result := 0
	for i := 0; i < b; i++ {
		result += a
	}
	return result
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	require.NoError(t, err)

	files := map[string]*ast.File{"test.go": f}
	reports := RunOnFiles(files, fset, 0.85, 5)

	assert.Empty(t, reports, "structurally different functions should not be flagged")
}

func TestRunOnFiles_AcrossFiles(t *testing.T) {
	srcA := `package p

func handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", 400)
		return
	}
	data := fetchData(id)
	if data == nil {
		http.Error(w, "not found", 404)
		return
	}
	json.NewEncoder(w).Encode(data)
}
`
	srcB := `package p

func handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, "missing id", 400)
		return
	}
	data := fetchData(id)
	if data == nil {
		http.Error(w, "not found", 404)
		return
	}
	json.NewEncoder(w).Encode(data)
}
`
	fset := token.NewFileSet()
	fA, err := parser.ParseFile(fset, "handlers_get.go", srcA, 0)
	require.NoError(t, err)
	fB, err := parser.ParseFile(fset, "handlers_delete.go", srcB, 0)
	require.NoError(t, err)

	files := map[string]*ast.File{
		"handlers_get.go":    fA,
		"handlers_delete.go": fB,
	}
	reports := RunOnFiles(files, fset, 0.85, 5)

	assert.NotEmpty(t, reports, "should detect duplicates across files")
}

func TestRunOnFiles_MinNodesFilter(t *testing.T) {
	src := `package p
func a() { x := 1; _ = x }
func b() { y := 1; _ = y }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	require.NoError(t, err)

	files := map[string]*ast.File{"test.go": f}

	// With high min-nodes, tiny functions are excluded
	reports := RunOnFiles(files, fset, 0.85, 100)
	assert.Empty(t, reports)

	// With low min-nodes, they are included
	reports = RunOnFiles(files, fset, 0.85, 1)
	assert.NotEmpty(t, reports)
}

func TestRunOnFiles_ThresholdSensitivity(t *testing.T) {
	// Two functions that are somewhat similar but not identical
	src := `package p

func funcA() {
	x := compute()
	if x > 0 {
		handle(x)
	}
	log(x)
}

func funcB() {
	x := compute()
	if x > 0 {
		handle(x)
		extra(x)
	}
	log(x)
	cleanup()
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	require.NoError(t, err)

	files := map[string]*ast.File{"test.go": f}

	// With a very high threshold, they should not match
	reportsHigh := RunOnFiles(files, fset, 0.99, 1)

	// With a low threshold, they should match
	reportsLow := RunOnFiles(files, fset, 0.50, 1)

	assert.LessOrEqual(t, len(reportsHigh), len(reportsLow),
		"lower threshold should find at least as many matches")
}

func TestRunOnFiles_IntraFunctionDuplicates(t *testing.T) {
	// Models the runBuildPhase pattern: if/else branches that both run
	// similar sequences of setup + exec with different arguments.
	src := `package p

func runBuildPhase(targets []string) {
	if len(targets) == 0 {
		args := []string{"build", "-o", "output"}
		args = append(args, defaultFlags()...)
		result := execute(args)
		if result != nil {
			log("build failed")
			return
		}
		log("build succeeded")
	} else {
		args := []string{"build", "-o", "output"}
		args = append(args, targetFlags(targets)...)
		result := execute(args)
		if result != nil {
			log("build failed")
			return
		}
		log("build succeeded")
	}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "build.go", src, 0)
	require.NoError(t, err)

	files := map[string]*ast.File{"build.go": f}
	reports := RunOnFiles(files, fset, 0.85, 5)

	require.NotEmpty(t, reports, "should detect near-duplicate if/else branches within a single function")
	r := reports[0]
	assert.Greater(t, r.Similarity, 0.85)
	assert.Contains(t, r.FuncA, "runBuildPhase/")
	assert.Contains(t, r.FuncB, "runBuildPhase/")
}

func TestRunOnFiles_IntraFunctionNoFalsePositive(t *testing.T) {
	// An if/else with genuinely different structure should not be flagged.
	// The if-branch uses assignments + calls + return; the else-branch
	// uses a for-loop + switch + defer — structurally very different.
	src := `package p

func handleResult(ok bool) {
	if ok {
		data := fetch()
		transform(data)
		save(data)
		log("success")
	} else {
		for i := 0; i < retries(); i++ {
			switch getStatus(i) {
			case 1:
				retry(i)
			case 2:
				defer cleanup(i)
			}
		}
	}
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	require.NoError(t, err)

	files := map[string]*ast.File{"test.go": f}
	reports := RunOnFiles(files, fset, 0.85, 5)

	assert.Empty(t, reports, "structurally different if/else branches should not be flagged")
}
