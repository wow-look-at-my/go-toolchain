package lint

import (
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestBuildSuggestion_IdenticalBlocks(t *testing.T) {
	tokens := []Token{
		{Symbol: 'I'}, {Symbol: '_', Concrete: "x"}, {Symbol: 'R'},
	}
	pair := DuplicatePair{
		A: &Block{Tokens: tokens, FuncName: "foo"},
		B: &Block{Tokens: tokens, FuncName: "bar"},
	}
	s := BuildSuggestion(pair)
	assert.Contains(t, s.Description, "foo")
	assert.Contains(t, s.Description, "bar")
	assert.Contains(t, s.Description, "identical")
}

func TestBuildSuggestion_WithDifferences(t *testing.T) {
	tokensA := []Token{
		{Symbol: 'I'}, {Symbol: '_', Concrete: "user"}, {Symbol: 'C'}, {Symbol: 'R'},
	}
	tokensB := []Token{
		{Symbol: 'I'}, {Symbol: '_', Concrete: "order"}, {Symbol: 'X'}, {Symbol: 'R'},
	}
	pair := DuplicatePair{
		A: &Block{Tokens: tokensA, FuncName: "processUser"},
		B: &Block{Tokens: tokensB, FuncName: "processOrder"},
	}
	s := BuildSuggestion(pair)
	assert.Contains(t, s.Description, "processUser")
	assert.Contains(t, s.Description, "processOrder")
	assert.NotEmpty(t, s.Parameters)
	// Should identify the differing values
	assert.Contains(t, s.Description, "user")
	assert.Contains(t, s.Description, "order")
}

func TestBuildSuggestion_MultipleDifferences(t *testing.T) {
	tokensA := []Token{
		{Symbol: 'A'}, {Symbol: '_', Concrete: "x"},
		{Symbol: 'I'}, {Symbol: '_', Concrete: "10"},
		{Symbol: 'C'}, {Symbol: '_', Concrete: "doX"},
		{Symbol: 'R'},
	}
	tokensB := []Token{
		{Symbol: 'A'}, {Symbol: '_', Concrete: "y"},
		{Symbol: 'I'}, {Symbol: '_', Concrete: "20"},
		{Symbol: 'C'}, {Symbol: '_', Concrete: "doY"},
		{Symbol: 'R'},
	}
	pair := DuplicatePair{
		A: &Block{Tokens: tokensA, FuncName: "funcA"},
		B: &Block{Tokens: tokensB, FuncName: "funcB"},
	}
	s := BuildSuggestion(pair)
	assert.Contains(t, s.Description, "Extract")
	// Should have parameters for the varying values
	assert.GreaterOrEqual(t, len(s.Parameters), 1)
}
