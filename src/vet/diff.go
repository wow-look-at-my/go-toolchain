package vet

import (
	"fmt"

	"github.com/pmezard/go-difflib/difflib"
)

// unifiedDiff renders a standard unified diff from old (the file's current,
// non-canonical content) to want (the canonical content the fixer computed),
// labeled with path. checkEditor attaches this to every violation it records so
// a CI failure names not just which files are wrong but exactly what change
// would fix them: the diff is unambiguous, machine-parseable, and directly
// consumable by `patch`/`git apply` -- which matters for a caller (human or
// agent) that can read this log but cannot run gofmt/go-toolchain itself to see
// the answer.
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
