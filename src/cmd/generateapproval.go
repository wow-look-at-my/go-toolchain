package cmd

import (
	"os"
	"strings"
)

// A dependency, or this tree, can ship a //go:generate directive and not its
// output. The build then has to run it, and running it executes code the
// approval hash is there to consent to. A flag cannot be that consent on its
// own: a fresh clone of such a repo has nobody to type it, so `go-toolchain`
// stops and asks for an argument before it can build anything at all.
//
// So the repo records its own consent, in a file it commits. A directive change
// moves the hash, which puts the next approval in a diff somebody reviews. That
// is what a flag typed once into CI never was.

// generateApprovalFile is where a repo records the hash it approved.
const generateApprovalFile = ".go-toolchain-generate"

// approvedGenerateHash answers the hash this run may execute directives for. The
// flag wins when given, so a one-off run needs no commit.
func approvedGenerateHash() string {
	if generateHash != "" {
		return generateHash
	}
	return readGenerateApproval(".")
}

// readGenerateApproval reads the recorded hash, and answers empty for a repo
// that records none. A line starting with # is a note rather than a hash.
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
