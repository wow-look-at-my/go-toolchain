package cmd

import (
	"os"
	"strings"
)

// Where a repo records the hash it approved. Depth: docs/PIPELINE.md
const generateApprovalFile = ".go-toolchain-generate"

// approvedGenerateHash prefers the flag, for a one-off run that needs no commit.
func approvedGenerateHash() string {
	if generateHash != "" {
		return generateHash
	}
	return readGenerateApproval(".")
}

// readGenerateApproval reads the recorded hash. A `#` line is a note.
func readGenerateApproval(root string) string {
	body, err := os.ReadFile(root + "/" + generateApprovalFile)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}
