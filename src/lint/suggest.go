package lint

import (
	"fmt"
	"strings"
)

// Suggestion describes a refactoring suggestion for a pair of near-duplicate blocks.
type Suggestion struct {
	Description string   `json:"description"`
	Parameters  []string `json:"parameters"`
}

// BuildSuggestion analyzes two near-duplicate blocks and produces a
// refactoring suggestion. It identifies the concrete values that differ
// between the two blocks — these become parameters of a proposed
// extracted function.
func BuildSuggestion(pair DuplicatePair) Suggestion {
	type paramPair struct {
		valueA string
		valueB string
	}

	seen := make(map[string]bool)
	var params []paramPair

	addParam := func(vA, vB string) {
		if vA == "" && vB == "" {
			return
		}
		key := vA + " -> " + vB
		if seen[key] {
			return
		}
		seen[key] = true
		params = append(params, paramPair{valueA: vA, valueB: vB})
	}

	// First: find concrete-value differences at structurally matched positions.
	// The LCS alignment tells us which indices are structurally paired.
	// Tokens that match structurally (same symbol) but have different Concrete
	// values represent the varying parameters we want to extract.
	matchedA, matchedB := lcsAlignment(pair.A.Tokens, pair.B.Tokens)
	for i := 0; i < len(matchedA); i++ {
		tA := pair.A.Tokens[matchedA[i]]
		tB := pair.B.Tokens[matchedB[i]]
		if tA.Concrete != tB.Concrete && (tA.Concrete != "" || tB.Concrete != "") {
			addParam(tA.Concrete, tB.Concrete)
		}
	}

	// Second: find structural diffs (positions not in the LCS).
	diffA, diffB := LCSDiff(pair.A.Tokens, pair.B.Tokens)
	minDiffs := len(diffA)
	if len(diffB) < minDiffs {
		minDiffs = len(diffB)
	}
	for i := 0; i < minDiffs; i++ {
		addParam(
			pair.A.Tokens[diffA[i]].Concrete,
			pair.B.Tokens[diffB[i]].Concrete,
		)
	}
	for i := minDiffs; i < len(diffA); i++ {
		v := pair.A.Tokens[diffA[i]].Concrete
		if v != "" {
			addParam(v, "")
		}
	}
	for i := minDiffs; i < len(diffB); i++ {
		v := pair.B.Tokens[diffB[i]].Concrete
		if v != "" {
			addParam("", v)
		}
	}

	var paramNames []string
	var details []string
	for i, p := range params {
		name := fmt.Sprintf("param%d", i+1)
		paramNames = append(paramNames, name)
		if p.valueA != "" && p.valueB != "" {
			details = append(details, fmt.Sprintf("  %s: %s vs %s", name, p.valueA, p.valueB))
		} else if p.valueA != "" {
			details = append(details, fmt.Sprintf("  %s: %s (only in first)", name, p.valueA))
		} else {
			details = append(details, fmt.Sprintf("  %s: %s (only in second)", name, p.valueB))
		}
	}

	var desc string
	if len(paramNames) > 0 {
		desc = fmt.Sprintf(
			"Extract common logic from %s and %s into a shared function with parameters: %s\nDiffering values:\n%s",
			pair.A.FuncName, pair.B.FuncName,
			strings.Join(paramNames, ", "),
			strings.Join(details, "\n"),
		)
	} else {
		desc = fmt.Sprintf(
			"Functions %s and %s are structurally identical and could be merged",
			pair.A.FuncName, pair.B.FuncName,
		)
	}

	return Suggestion{
		Description: desc,
		Parameters:  paramNames,
	}
}

// lcsAlignment returns paired indices: matchedA[i] in A corresponds to
// matchedB[i] in B via the LCS alignment.
func lcsAlignment(a, b []Token) (matchedA, matchedB []int) {
	sa := make([]byte, len(a))
	sb := make([]byte, len(b))
	for i, t := range a {
		sa[i] = t.Symbol
	}
	for i, t := range b {
		sb[i] = t.Symbol
	}

	m, n := len(sa), len(sb)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := 1; i <= m; i++ {
		for j := 1; j <= n; j++ {
			if sa[i-1] == sb[j-1] {
				dp[i][j] = dp[i-1][j-1] + 1
			} else {
				dp[i][j] = dp[i-1][j]
				if dp[i][j-1] > dp[i][j] {
					dp[i][j] = dp[i][j-1]
				}
			}
		}
	}

	// Backtrack to get matched pairs.
	i, j := m, n
	for i > 0 && j > 0 {
		if sa[i-1] == sb[j-1] {
			matchedA = append(matchedA, i-1)
			matchedB = append(matchedB, j-1)
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	// Reverse to get forward order.
	for l, r := 0, len(matchedA)-1; l < r; l, r = l+1, r-1 {
		matchedA[l], matchedA[r] = matchedA[r], matchedA[l]
		matchedB[l], matchedB[r] = matchedB[r], matchedB[l]
	}

	return matchedA, matchedB
}
