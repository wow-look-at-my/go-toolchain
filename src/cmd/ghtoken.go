package cmd

import (
	"os"
	"strings"
)

// ghTokenPrefixes are the known prefixes for GitHub personal access tokens
// and other GitHub-issued tokens.
var ghTokenPrefixes = []string{
	"ghp_",        // classic PATs
	"github_pat_", // fine-grained PATs
	"gho_",        // OAuth access tokens
	"ghu_",        // user-to-server tokens
	"ghs_",        // server-to-server tokens
	"ghr_",        // refresh tokens
}

// wellKnownTokenVars are environment variables commonly used to store GitHub tokens.
var wellKnownTokenVars = []string{
	"GITHUB_TOKEN",
	"GH_TOKEN",
	"GITHUB_PAT",
	"GH_PAT",
}

// environFunc returns the environment variables. Replaceable for testing.
var environFunc = os.Environ

// discoverGitHubToken searches the environment for a GitHub personal access
// token. It checks well-known variable names first, then scans all env vars
// for values that look like GitHub-issued tokens.
//
// This is used ONLY for the non-destructive, read-only action of checking
// for go-toolchain updates via the GitHub releases API, which is subject to
// aggressive rate limiting for unauthenticated requests.
func discoverGitHubToken() string {
	// 1. Check well-known env vars first.
	for _, name := range wellKnownTokenVars {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}

	// 2. Scan all env vars for values that look like GitHub tokens.
	for _, entry := range environFunc() {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		val := parts[1]
		for _, prefix := range ghTokenPrefixes {
			if strings.HasPrefix(val, prefix) {
				return val
			}
		}
	}

	return ""
}
