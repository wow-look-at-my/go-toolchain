package vet

import (
	"fmt"

	"github.com/pmezard/go-difflib/difflib"
)

// unifiedDiff renders a unified diff from old (current content) to want (canonical content), labeled with
// path. checkEditor attaches this to every violation, so CI names exactly what change would fix it --
// consumable by `patch`/`git apply` for a reader that cannot run go-toolchain to see the answer.
func unifiedDiff(path string, old, want []byte) (string, error) {
	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(old)),
		B:        difflib.SplitLines(string(want)),
		FromFile: path,
		ToFile:   path,
		FromDate: "current",
		ToDate:   "canonical",
		Context:  3,
	}
	text, err := difflib.GetUnifiedDiffString(diff)
	if err != nil {
		return "", fmt.Errorf("diffing %s: %w", path, err)
	}
	return text, nil
}
