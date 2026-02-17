package lint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
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
	tests := []struct {
		node     ast.Node
		expected string
	}{
		{&ast.IfStmt{}, "IfStmt"},
		{&ast.ForStmt{}, "ForStmt"},
		{&ast.RangeStmt{}, "RangeStmt"},
		{&ast.SwitchStmt{}, "SwitchStmt"},
		{&ast.TypeSwitchStmt{}, "TypeSwitchStmt"},
		{&ast.SelectStmt{}, "SelectStmt"},
		{&ast.CaseClause{}, "CaseClause"},
		{&ast.CommClause{}, "CommClause"},
		{&ast.ReturnStmt{}, "ReturnStmt"},
		{&ast.AssignStmt{}, "AssignStmt"},
		{&ast.DeclStmt{}, "DeclStmt"},
		{&ast.ExprStmt{}, "ExprStmt"},
		{&ast.GoStmt{}, "GoStmt"},
		{&ast.DeferStmt{}, "DeferStmt"},
		{&ast.SendStmt{}, "SendStmt"},
		{&ast.IncDecStmt{}, "IncDecStmt"},
		{&ast.BranchStmt{}, "BranchStmt"},
		{&ast.LabeledStmt{}, "LabeledStmt"},
		{&ast.BlockStmt{}, "BlockStmt"},
		{&ast.CallExpr{}, "CallExpr"},
		{&ast.UnaryExpr{}, "UnaryExpr"},
		{&ast.BinaryExpr{}, "BinaryExpr"},
		{&ast.IndexExpr{}, "IndexExpr"},
		{&ast.SliceExpr{}, "SliceExpr"},
		{&ast.TypeAssertExpr{}, "TypeAssertExpr"},
		{&ast.KeyValueExpr{}, "KeyValueExpr"},
		{&ast.CompositeLit{}, "CompositeLit"},
		{&ast.FuncLit{Body: &ast.BlockStmt{}}, "FuncLit"},
		{&ast.SelectorExpr{}, "SelectorExpr"},
		{&ast.StarExpr{}, "StarExpr"},
		{&ast.File{}, ""},  // unmapped type
		{&ast.Field{}, ""}, // unmapped type
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, nodeTypeName(tt.node))
	}
}

func TestLinearize_AllNodeTypes(t *testing.T) {
	// Exercise as many AST node types as possible in a single function
	src := `package p

import "fmt"

func allNodes() {
	// AssignStmt, BinaryExpr, BasicLit, Ident
	x := 1 + 2
	// IncDecStmt
	x++
	// ExprStmt, CallExpr, SelectorExpr
	fmt.Println(x)
	// IfStmt
	if x > 0 {
		// ReturnStmt
		return
	}
	// ForStmt, BlockStmt
	for i := 0; i < x; i++ {
		_ = i
	}
	// RangeStmt
	for _, v := range []int{1, 2} {
		_ = v
	}
	// SwitchStmt, CaseClause
	switch x {
	case 1:
		_ = x
	}
	// DeferStmt
	defer fmt.Println("done")
	// UnaryExpr, StarExpr
	p := &x
	_ = *p
	// CompositeLit, KeyValueExpr
	m := map[string]int{"a": 1}
	// IndexExpr
	_ = m["a"]
	// SliceExpr
	s := []int{1, 2, 3}
	_ = s[0:1]
	// FuncLit
	f := func() {}
	f()
	// GoStmt
	go f()
	// DeclStmt
	var y int
	_ = y
	// TypeAssertExpr
	var iface interface{} = 42
	_ = iface.(int)
	// SendStmt
	ch := make(chan int, 1)
	ch <- 1
	// SelectStmt, CommClause
	select {
	case v := <-ch:
		_ = v
	}
	// BranchStmt
	for {
		break
	}
	// LabeledStmt
done:
	_ = x
	goto done
}
`
	f, fset := parseSource(t, src)
	blocks := ExtractBlocks(f, fset, 1)
	require.NotEmpty(t, blocks)

	seq := blocks[0].Sequence
	// Verify key structural symbols are present
	assert.Contains(t, seq, "A") // AssignStmt
	assert.Contains(t, seq, "N") // IncDecStmt
	assert.Contains(t, seq, "X") // ExprStmt
	assert.Contains(t, seq, "C") // CallExpr
	assert.Contains(t, seq, ".") // SelectorExpr
	assert.Contains(t, seq, "I") // IfStmt
	assert.Contains(t, seq, "R") // ReturnStmt
	assert.Contains(t, seq, "F") // ForStmt
	assert.Contains(t, seq, "G") // RangeStmt
	assert.Contains(t, seq, "W") // SwitchStmt
	assert.Contains(t, seq, "K") // CaseClause
	assert.Contains(t, seq, "P") // DeferStmt
	assert.Contains(t, seq, "U") // UnaryExpr
	assert.Contains(t, seq, "*") // StarExpr
	assert.Contains(t, seq, "Q") // CompositeLit
	assert.Contains(t, seq, "H") // KeyValueExpr
	assert.Contains(t, seq, "J") // IndexExpr
	assert.Contains(t, seq, "Z") // SliceExpr
	assert.Contains(t, seq, "#") // FuncLit
	assert.Contains(t, seq, "O") // GoStmt
	assert.Contains(t, seq, "D") // DeclStmt
	assert.Contains(t, seq, "T") // TypeAssertExpr
	assert.Contains(t, seq, "S") // SendStmt
	assert.Contains(t, seq, "E") // SelectStmt
	assert.Contains(t, seq, "M") // CommClause
	assert.Contains(t, seq, "B") // BranchStmt
	assert.Contains(t, seq, "L") // LabeledStmt
	assert.Contains(t, seq, "V") // BinaryExpr
}
