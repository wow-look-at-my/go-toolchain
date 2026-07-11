package lint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSimilarity_Identical(t *testing.T) {
	assert.InDelta(t, 1.0, Similarity("IACR", "IACR"), 0.001)
}

func TestSimilarity_Empty(t *testing.T) {
	assert.InDelta(t, 1.0, Similarity("", ""), 0.001)
	assert.InDelta(t, 0.0, Similarity("ABC", ""), 0.001)
	assert.InDelta(t, 0.0, Similarity("", "ABC"), 0.001)
}

func TestSimilarity_CompletelyDifferent(t *testing.T) {
	// No common subsequence at all
	assert.InDelta(t, 0.0, Similarity("ABC", "XYZ"), 0.001)
}

func TestSimilarity_PartialMatch(t *testing.T) {
	// "ABCDE" vs "ABXDE" — LCS is "ABDE" (length 4)
	// similarity = 2*4 / (5+5) = 0.8
	sim := Similarity("ABCDE", "ABXDE")
	assert.InDelta(t, 0.8, sim, 0.001)
}

func TestSimilarity_Symmetric(t *testing.T) {
	a, b := "I_C_R", "I_X_R"
	assert.InDelta(t, Similarity(a, b), Similarity(b, a), 0.001)
}

func TestSimilarity_HighSimilarity(t *testing.T) {
	// Two sequences differing by one character out of 10
	a := "IA_C_V_R_X"
	b := "IA_C_V_R_Y"
	sim := Similarity(a, b)
	assert.Greater(t, sim, 0.85, "should be above default threshold")
}

func TestLCSLength(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int
	}{
		{"", "", 0},
		{"ABC", "", 0},
		{"ABC", "ABC", 3},
		{"ABCDE", "ACE", 3},
		{"ABCBDAB", "BDCABA", 4},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, lcsLength(tt.a, tt.b), "lcsLength(%q, %q)", tt.a, tt.b)
	}
}

func TestLCSDiff(t *testing.T) {
	a := []Token{
		{Symbol: 'I'}, {Symbol: '_', Concrete: "x"}, {Symbol: 'R'},
	}
	b := []Token{
		{Symbol: 'I'}, {Symbol: '_', Concrete: "y"}, {Symbol: 'R'},
	}
	diffA, diffB := LCSDiff(a, b)
	// All symbols are identical (I, _, R) so LCS covers everything
	// and there should be no structural diffs
	assert.Empty(t, diffA)
	assert.Empty(t, diffB)
}

func TestLCSDiff_StructuralDifference(t *testing.T) {
	a := []Token{
		{Symbol: 'I'}, {Symbol: 'C'}, {Symbol: 'R'},
	}
	b := []Token{
		{Symbol: 'I'}, {Symbol: 'X'}, {Symbol: 'R'},
	}
	diffA, diffB := LCSDiff(a, b)
	// Position 1 differs: C vs X
	assert.Equal(t, []int{1}, diffA)
	assert.Equal(t, []int{1}, diffB)
}

func TestFindDuplicates_NoDuplicates(t *testing.T) {
	blocks := map[string][]Block{
		"a.go": {
			{Tokens: makeTokens("IACR_IACR_IACR_IACR_IACR"), Sequence: "IACR_IACR_IACR_IACR_IACR", FuncName: "foo"},
		},
		"b.go": {
			{Tokens: makeTokens("XYZW_XYZW_XYZW_XYZW_XYZW"), Sequence: "XYZW_XYZW_XYZW_XYZW_XYZW", FuncName: "bar"},
		},
	}
	pairs := FindDuplicates(blocks, 0.85)
	assert.Empty(t, pairs)
}

func TestFindDuplicates_IdenticalBlocks(t *testing.T) {
	seq := "IA_CV_R_IA_CV_R_IA_CV_R_"
	blocks := map[string][]Block{
		"a.go": {
			{Tokens: makeTokens(seq), Sequence: seq, FuncName: "foo"},
		},
		"b.go": {
			{Tokens: makeTokens(seq), Sequence: seq, FuncName: "bar"},
		},
	}
	pairs := FindDuplicates(blocks, 0.85)
	assert.Len(t, pairs, 1)
	assert.InDelta(t, 1.0, pairs[0].Similarity, 0.001)
}

func TestFindDuplicates_SizeFilter(t *testing.T) {
	// Blocks of very different sizes should not be compared
	short := "IA"
	long := "IACR_IACR_IACR_IACR_IACR_IACR_IACR_IACR_IACR_IACR"
	blocks := map[string][]Block{
		"a.go": {
			{Tokens: makeTokens(short), Sequence: short, FuncName: "small"},
		},
		"b.go": {
			{Tokens: makeTokens(long), Sequence: long, FuncName: "big"},
		},
	}
	pairs := FindDuplicates(blocks, 0.85)
	assert.Empty(t, pairs, "very different sized blocks should not match")
}

func makeTokens(seq string) []Token {
	tokens := make([]Token, len(seq))
	for i, c := range seq {
		tokens[i] = Token{Symbol: byte(c)}
	}
	return tokens
}
