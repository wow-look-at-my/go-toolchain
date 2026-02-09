package lint

import (
	"go/ast"
	"go/token"
	"strings"
)

// Token represents a single element in a linearized AST sequence.
// Structural tokens use a single-char symbol; leaf nodes (identifiers,
// literals, types) are recorded as placeholder "_" but the concrete
// value is preserved for refactoring suggestions.
type Token struct {
	Symbol   byte   // structural symbol (e.g. 'I' for IfStmt)
	Concrete string // original name/literal for leaf nodes, empty for structural
}

// Block is a linearized representation of a code block (function body,
// if-body, etc.) together with source location metadata.
type Block struct {
	Tokens   []Token
	Sequence string // the symbols concatenated for fast comparison
	FuncName string
	Pos      token.Pos
	End      token.Pos
}

// NodeCount returns the number of tokens in the block.
func (b *Block) NodeCount() int {
	return len(b.Tokens)
}

// AST node type → symbol mapping.
// We only preserve structural shape; all concrete values become '_'.
var nodeSymbols = map[string]byte{
	"IfStmt":         'I',
	"ForStmt":        'F',
	"RangeStmt":      'G', // ranGe
	"SwitchStmt":     'W',
	"TypeSwitchStmt": 'Y',
	"SelectStmt":     'E',
	"CaseClause":     'K',
	"CommClause":     'M',
	"ReturnStmt":     'R',
	"AssignStmt":     'A',
	"DeclStmt":       'D',
	"ExprStmt":       'X',
	"GoStmt":         'O',
	"DeferStmt":      'P',
	"SendStmt":       'S',
	"IncDecStmt":     'N',
	"BranchStmt":     'B',
	"LabeledStmt":    'L',
	"BlockStmt":      '{',
	"CallExpr":       'C',
	"UnaryExpr":      'U',
	"BinaryExpr":     'V',
	"IndexExpr":      'J',
	"SliceExpr":      'Z',
	"TypeAssertExpr": 'T',
	"KeyValueExpr":   'H',
	"CompositeLit":   'Q',
	"FuncLit":        '#',
	"SelectorExpr":   '.',
	"StarExpr":       '*',
}

// Linearize converts an AST node (typically a function body) into a
// sequence of abstract tokens, stripping all concrete identifiers,
// literals, and type names while preserving structural shape.
func Linearize(node ast.Node) []Token {
	var tokens []Token
	ast.Inspect(node, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		switch v := n.(type) {
		case *ast.Ident:
			tokens = append(tokens, Token{Symbol: '_', Concrete: v.Name})
			return false
		case *ast.BasicLit:
			tokens = append(tokens, Token{Symbol: '_', Concrete: v.Value})
			return false
		default:
			typeName := nodeTypeName(n)
			if sym, ok := nodeSymbols[typeName]; ok {
				tokens = append(tokens, Token{Symbol: sym})
			}
			// Continue into children for structural nodes
			return true
		}
	})
	return tokens
}

// SequenceString builds the compact symbol string from a token slice.
func SequenceString(tokens []Token) string {
	var b strings.Builder
	b.Grow(len(tokens))
	for _, t := range tokens {
		b.WriteByte(t.Symbol)
	}
	return b.String()
}

// ExtractBlocks walks a file AST and extracts all function/method bodies
// as linearized blocks. Only blocks with at least minNodes tokens are
// returned, since very small blocks are uninteresting for duplication.
func ExtractBlocks(file *ast.File, fset *token.FileSet, minNodes int) []Block {
	var blocks []Block
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		tokens := Linearize(fn.Body)
		if len(tokens) < minNodes {
			continue
		}
		blocks = append(blocks, Block{
			Tokens:   tokens,
			Sequence: SequenceString(tokens),
			FuncName: fn.Name.Name,
			Pos:      fn.Pos(),
			End:      fn.End(),
		})
	}
	return blocks
}

// nodeTypeName returns the short type name of an AST node
// (e.g., "IfStmt" from "*ast.IfStmt").
func nodeTypeName(n ast.Node) string {
	// Use a type switch for the types we care about, which is faster
	// and avoids reflection.
	switch n.(type) {
	case *ast.IfStmt:
		return "IfStmt"
	case *ast.ForStmt:
		return "ForStmt"
	case *ast.RangeStmt:
		return "RangeStmt"
	case *ast.SwitchStmt:
		return "SwitchStmt"
	case *ast.TypeSwitchStmt:
		return "TypeSwitchStmt"
	case *ast.SelectStmt:
		return "SelectStmt"
	case *ast.CaseClause:
		return "CaseClause"
	case *ast.CommClause:
		return "CommClause"
	case *ast.ReturnStmt:
		return "ReturnStmt"
	case *ast.AssignStmt:
		return "AssignStmt"
	case *ast.DeclStmt:
		return "DeclStmt"
	case *ast.ExprStmt:
		return "ExprStmt"
	case *ast.GoStmt:
		return "GoStmt"
	case *ast.DeferStmt:
		return "DeferStmt"
	case *ast.SendStmt:
		return "SendStmt"
	case *ast.IncDecStmt:
		return "IncDecStmt"
	case *ast.BranchStmt:
		return "BranchStmt"
	case *ast.LabeledStmt:
		return "LabeledStmt"
	case *ast.BlockStmt:
		return "BlockStmt"
	case *ast.CallExpr:
		return "CallExpr"
	case *ast.UnaryExpr:
		return "UnaryExpr"
	case *ast.BinaryExpr:
		return "BinaryExpr"
	case *ast.IndexExpr:
		return "IndexExpr"
	case *ast.SliceExpr:
		return "SliceExpr"
	case *ast.TypeAssertExpr:
		return "TypeAssertExpr"
	case *ast.KeyValueExpr:
		return "KeyValueExpr"
	case *ast.CompositeLit:
		return "CompositeLit"
	case *ast.FuncLit:
		return "FuncLit"
	case *ast.SelectorExpr:
		return "SelectorExpr"
	case *ast.StarExpr:
		return "StarExpr"
	default:
		return ""
	}
}
