package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

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

	assert.Len(t, reports, 1, "should detect one duplicate pair")
	if len(reports) > 0 {
		r := reports[0]
		assert.Greater(t, r.Similarity, 0.85)
		assert.Contains(t, []string{r.FuncA, r.FuncB}, "processUser")
		assert.Contains(t, []string{r.FuncA, r.FuncB}, "processOrder")
	}
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
