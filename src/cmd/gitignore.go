package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/memlimit"
)

// ensureBuildDirInGitignore adds the build output directory to .gitignore
// when inside a git repo and not already ignored. Best-effort: errors are
// silently ignored so they never block the build.
func ensureBuildDirInGitignore() {
	ensureGitignored("/" + outputDir + "/")
	// Strip a stale GOMEMLIMIT guard entry an older version left behind (now excluded via checkDirtyInCI instead).
	removeFromGitignore(memlimit.GuardFileName)
}

// removeFromGitignore deletes every line of the repository's .gitignore that
// exactly equals entry (ignoring surrounding whitespace), rewriting the file
// only when something was actually removed. It is the inverse of
// ensureGitignored and exists to undo an entry an older go-toolchain wrote that
// the current release must not keep — namely the transient GOMEMLIMIT guard
// filename. Best-effort: any error is silently ignored so it never blocks the
// build.
func removeFromGitignore(entry string) {
	gitRoot := findGitRoot()
	if gitRoot == "" {
		return
	}
	gitignorePath := filepath.Join(gitRoot, ".gitignore")
	data, err := os.ReadFile(gitignorePath)
	if err != nil {
		return
	}
	lines := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(lines))
	removed := false
	for _, line := range lines {
		if strings.TrimSpace(line) == entry {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return
	}
	// Split/Join round-trips a trailing newline as a final "" element, so it's preserved with no special handling.
	_ = os.WriteFile(gitignorePath, []byte(strings.Join(kept, "\n")), 0644)
}

// ensureGitignored appends entry to the repository's .gitignore when the
// current directory is inside a git repo and the entry isn't already present.
// It's a best-effort operation: any error is silently ignored so it never
// blocks the build.
func ensureGitignored(entry string) {
	gitRoot := findGitRoot()
	if gitRoot == "" {
		return
	}

	gitignorePath := filepath.Join(gitRoot, ".gitignore")
	if gitignoreContains(gitignorePath, entry) {
		return
	}

	f, err := os.OpenFile(gitignorePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer f.Close()

	// If the file is non-empty and doesn't end with a newline, add the newline before appending.
	if needsLeadingNewline(gitignorePath) {
		fmt.Fprint(f, "\n")
	}
	fmt.Fprintln(f, entry)
}

// findGitRoot walks up from the current directory looking for a .git entry.
func findGitRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if fi, err := os.Stat(filepath.Join(dir, ".git")); err == nil && fi.IsDir() {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// gitignoreContains reports whether the .gitignore at path already
// contains a line that would ignore the build output directory.
func gitignoreContains(path, entry string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	// Accept common variants: "build/", "/build/", "build", "/build"
	base := strings.TrimPrefix(entry, "/")
	base = strings.TrimSuffix(base, "/")

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		trimmed := strings.TrimPrefix(line, "/")
		trimmed = strings.TrimSuffix(trimmed, "/")
		if trimmed == base {
			return true
		}
	}
	return false
}

// needsLeadingNewline reports whether the file exists and doesn't end
// with a newline character.
func needsLeadingNewline(path string) bool {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 1)
	if _, err := f.ReadAt(buf, fi.Size()-1); err != nil {
		return false
	}
	return buf[0] != '\n'
}
