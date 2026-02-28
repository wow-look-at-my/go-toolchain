package lint

// DuplicatePair records two blocks that are near-duplicates.
type DuplicatePair struct {
	A          *Block
	B          *Block
	FileA      string
	FileB      string
	Similarity float64
}

// FindDuplicates compares all candidate blocks across all files and returns
// pairs whose similarity exceeds the threshold. Blocks are pre-bucketed by
// approximate size to avoid O(n²) full comparisons.
func FindDuplicates(fileBlocks map[string][]Block, threshold float64) []DuplicatePair {
	// Flatten all blocks with file info for bucketing.
	type entry struct {
		file  string
		block *Block
	}

	var all []entry
	for file, blocks := range fileBlocks {
		for i := range blocks {
			all = append(all, entry{file: file, block: &blocks[i]})
		}
	}

	// Bucket by approximate token count. Two blocks are only compared
	// if their sizes fall in overlapping buckets. Bucket width is chosen
	// so that blocks differing by more than 50% in size are never
	// compared (they can't reach 0.85 similarity anyway).
	const bucketWidth = 8
	buckets := make(map[int][]int) // bucket key -> indices into `all`
	for i, e := range all {
		key := e.block.NodeCount() / bucketWidth
		buckets[key] = append(buckets[key], i)
		// Also add to adjacent bucket so size-boundary blocks are compared.
		buckets[key+1] = append(buckets[key+1], i)
	}

	// Track which pairs we've already compared to avoid duplicates
	// from the overlapping bucket assignment.
	type pairKey struct{ a, b int }
	seen := make(map[pairKey]bool)

	var pairs []DuplicatePair

	for _, indices := range buckets {
		for i := 0; i < len(indices); i++ {
			for j := i + 1; j < len(indices); j++ {
				ai, bi := indices[i], indices[j]
				if ai > bi {
					ai, bi = bi, ai
				}
				k := pairKey{ai, bi}
				if seen[k] {
					continue
				}
				seen[k] = true

				ea, eb := all[ai], all[bi]

				// Skip pairs where one block contains the other
				// (e.g. function body vs its inner if-block).
				if ea.file == eb.file && posContains(ea.block, eb.block) {
					continue
				}

				// Quick length-ratio pre-filter.
				la, lb := len(ea.block.Sequence), len(eb.block.Sequence)
				if la == 0 || lb == 0 {
					continue
				}
				ratio := float64(la) / float64(lb)
				if ratio < threshold || 1.0/ratio < threshold {
					continue
				}

				sim := Similarity(ea.block.Sequence, eb.block.Sequence)
				if sim >= threshold {
					pairs = append(pairs, DuplicatePair{
						A:          ea.block,
						B:          eb.block,
						FileA:      ea.file,
						FileB:      eb.file,
						Similarity: sim,
					})
				}
			}
		}
	}

	return pairs
}

// Similarity computes the similarity between two symbol sequences using
// the longest common subsequence (LCS). Returns a value in [0.0, 1.0].
// Similarity = 2 * LCS_length / (len(a) + len(b)).
func Similarity(a, b string) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	lcs := lcsLength(a, b)
	return 2.0 * float64(lcs) / float64(len(a)+len(b))
}

// lcsLength computes the length of the longest common subsequence
// using O(min(m,n)) space via two-row DP.
func lcsLength(a, b string) int {
	// Ensure a is the shorter string for space efficiency.
	if len(a) > len(b) {
		a, b = b, a
	}
	m, n := len(a), len(b)

	prev := make([]int, m+1)
	curr := make([]int, m+1)

	for j := 1; j <= n; j++ {
		for i := 1; i <= m; i++ {
			if a[i-1] == b[j-1] {
				curr[i] = prev[i-1] + 1
			} else {
				curr[i] = prev[i]
				if curr[i-1] > curr[i] {
					curr[i] = curr[i-1]
				}
			}
		}
		prev, curr = curr, prev
		// Clear curr for next row
		for i := range curr {
			curr[i] = 0
		}
	}

	return prev[m]
}

// LCSDiff computes the actual LCS alignment and returns the indices
// in each sequence where they differ (i.e., positions not part of the LCS).
// These differing positions correspond to the concrete values that vary
// between two near-duplicate blocks.
func LCSDiff(a, b []Token) (diffA, diffB []int) {
	sa := make([]byte, len(a))
	sb := make([]byte, len(b))
	for i, t := range a {
		sa[i] = t.Symbol
	}
	for i, t := range b {
		sb[i] = t.Symbol
	}

	m, n := len(sa), len(sb)
	// Full DP table for backtracking.
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

	// Backtrack to find which positions are NOT in the LCS.
	matchA := make([]bool, m)
	matchB := make([]bool, n)
	i, j := m, n
	for i > 0 && j > 0 {
		if sa[i-1] == sb[j-1] {
			matchA[i-1] = true
			matchB[j-1] = true
			i--
			j--
		} else if dp[i-1][j] >= dp[i][j-1] {
			i--
		} else {
			j--
		}
	}

	for idx := 0; idx < m; idx++ {
		if !matchA[idx] {
			diffA = append(diffA, idx)
		}
	}
	for idx := 0; idx < n; idx++ {
		if !matchB[idx] {
			diffB = append(diffB, idx)
		}
	}
	return diffA, diffB
}

// posContains returns true if one block's source range entirely contains
// the other's. This detects parent/child relationships (e.g. a function
// body containing an if-block).
func posContains(a, b *Block) bool {
	return (a.Pos <= b.Pos && a.End >= b.End) ||
		(b.Pos <= a.Pos && b.End >= a.End)
}
