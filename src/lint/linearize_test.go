package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func parseSource(t *testing.T, src string) (*ast.File, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, 0)
	require.NoError(t, err)
	return f, fset
}

func TestLinearize_SimpleFunction(t *testing.T) {
	src := `package p
func foo() {
	x := 1
	if x > 0 {
		return
	}
}`
	f, _ := parseSource(t, src)
	fn := f.Decls[0].(*ast.FuncDecl)
	tokens := Linearize(fn.Body)

	// Should contain structural tokens and leaf placeholders
	assert.NotEmpty(t, tokens)

	// Check that we have expected structural symbols
	seq := SequenceString(tokens)
	assert.Contains(t, seq, "A") // AssignStmt for x := 1
	assert.Contains(t, seq, "I") // IfStmt
	assert.Contains(t, seq, "R") // ReturnStmt
}

func TestLinearize_PreservesConcreteValues(t *testing.T) {
	src := `package p
func foo() {
	x := 42
}`
	f, _ := parseSource(t, src)
	fn := f.Decls[0].(*ast.FuncDecl)
	tokens := Linearize(fn.Body)

	// Find the leaf tokens with concrete values
	var concretes []string
	for _, tok := range tokens {
		if tok.Concrete != "" {
			concretes = append(concretes, tok.Concrete)
		}
	}
	assert.Contains(t, concretes, "x")
	assert.Contains(t, concretes, "42")
}

func TestLinearize_StripsConcretesFromSymbols(t *testing.T) {
	// Two functions with same structure but different names/literals
	// should produce identical symbol sequences.
	src := `package p
func foo() {
	x := 1
	if x > 0 {
		println(x)
	}
}
func bar() {
	y := 2
	if y > 0 {
		println(y)
	}
}`
	f, _ := parseSource(t, src)

	fn1 := f.Decls[0].(*ast.FuncDecl)
	fn2 := f.Decls[1].(*ast.FuncDecl)

	seq1 := SequenceString(Linearize(fn1.Body))
	seq2 := SequenceString(Linearize(fn2.Body))

	assert.Equal(t, seq1, seq2, "structurally identical functions should have the same symbol sequence")
}

func TestLinearize_DifferentStructures(t *testing.T) {
	src := `package p
func foo() {
	if true {
		return
	}
}
func bar() {
	for i := 0; i < 10; i++ {
		println(i)
	}
}`
	f, _ := parseSource(t, src)

	fn1 := f.Decls[0].(*ast.FuncDecl)
	fn2 := f.Decls[1].(*ast.FuncDecl)

	seq1 := SequenceString(Linearize(fn1.Body))
	seq2 := SequenceString(Linearize(fn2.Body))

	assert.NotEqual(t, seq1, seq2, "structurally different functions should have different sequences")
}

func TestExtractBlocks_MinNodes(t *testing.T) {
	src := `package p
func tiny() { return }
func bigger() {
	x := 1
	y := 2
	z := x + y
	if z > 0 {
		println(z)
	}
	for i := 0; i < z; i++ {
		println(i)
	}
}`
	f, fset := parseSource(t, src)

	// With a high minNodes, only the bigger function should be included
	blocks := ExtractBlocks(f, fset, 10)
	assert.Len(t, blocks, 1)
	assert.Equal(t, "bigger", blocks[0].FuncName)

	// With minNodes=1, both should be included
	blocks = ExtractBlocks(f, fset, 1)
	assert.Len(t, blocks, 2)
}

func TestExtractBlocks_FuncName(t *testing.T) {
	src := `package p
func alpha() { x := 1; _ = x }
func beta() { y := 2; _ = y }
`
	f, fset := parseSource(t, src)
	blocks := ExtractBlocks(f, fset, 1)

	names := make([]string, len(blocks))
	for i, b := range blocks {
		names[i] = b.FuncName
	}
	assert.Contains(t, names, "alpha")
	assert.Contains(t, names, "beta")
}

func TestSequenceString(t *testing.T) {
	tokens := []Token{
		{Symbol: 'I'},
		{Symbol: '_', Concrete: "x"},
		{Symbol: 'R'},
	}
	assert.Equal(t, "I_R", SequenceString(tokens))
}

func TestNodeTypeName(t *testing.T) {
	assert.Equal(t, "IfStmt", nodeTypeName(&ast.IfStmt{}))
	assert.Equal(t, "ForStmt", nodeTypeName(&ast.ForStmt{}))
	assert.Equal(t, "CallExpr", nodeTypeName(&ast.CallExpr{}))
	assert.Equal(t, "ReturnStmt", nodeTypeName(&ast.ReturnStmt{}))
	assert.Equal(t, "", nodeTypeName(&ast.File{})) // unmapped type
}
