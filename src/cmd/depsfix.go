package cmd

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
)

// FixBogusDepsVersions detects dependencies with v0.0.0 versions in go.mod and
// resolves them to actual pseudo-versions. This happens when someone adds a
// git-based dependency without a proper version tag.
func FixBogusDepsVersions(r runner.CommandRunner) error {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return nil // Let go mod tidy handle missing go.mod
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil // Let go mod tidy handle parse errors
	}

	var toFix []string
	for _, req := range f.Require {
		if req.Mod.Version == "v0.0.0" {
			toFix = append(toFix, req.Mod.Path)
		}
	}

	if len(toFix) == 0 {
		return nil
	}

	// Resolve each module to its actual latest version
	for _, mod := range toFix {
		if !jsonOutput {
			logger.Info("⇒ Resolving %s (v0.0.0 is not a valid version)", mod)
		}

		version, err := resolveLatestVersionViaGit(r, mod)
		if err != nil {
			return fmt.Errorf("failed to resolve %s: %w", mod, err)
		}

		// Update the require in the parsed file
		if err := f.AddRequire(mod, version); err != nil {
			return fmt.Errorf("failed to update %s: %w", mod, err)
		}
	}

	// Write the updated go.mod
	newData, err := f.Format()
	if err != nil {
		return fmt.Errorf("failed to format go.mod: %w", err)
	}
	if err := os.WriteFile("go.mod", newData, 0644); err != nil {
		return fmt.Errorf("failed to write go.mod: %w", err)
	}

	return nil
}

// resolveLatestVersionViaGit fetches the latest commit from a git repo's
// default branch and constructs a proper pseudo-version with the correct
// timestamp.
func resolveLatestVersionViaGit(r runner.CommandRunner, mod string) (string, error) {
	return resolveVersionViaGit(r, mod, "HEAD")
}

// resolveGitURLAndRef discovers mod's git repository and the ref's ls-remote
// output in one pass. It tries the full module path as the git URL first --
// the common case, a module at its repo's root -- and, only if THAT URL is
// not a reachable repository at all, backs off one path segment at a time
// and retries. A module living in a subdirectory of its repository
// (agentic-loop's go/ submodule is exactly this shape -- the repo also has a
// ts/ directory) needs this: its import path is longer than its actual repo
// root, and everything past the root is a path INSIDE the repo, not part of
// the git URL. Segment-by-segment backoff works for any host -- GitHub and
// Bitbucket's fixed owner/repo shape, GitLab's arbitrarily nested subgroups,
// a self-hosted server -- without a hardcoded table of which hosts have
// which shape, and without ever having to update that table for a host it
// didn't know about.
//
// Backoff triggers ONLY on a git-level failure (the URL isn't a repository:
// git ls-remote exits non-zero). A URL that IS a repository but where ref
// simply doesn't exist there is git ls-remote's normal, successful, empty
// result -- treated as final, not as a reason to keep guessing shorter
// prefixes that could accidentally match an unrelated repository that
// happens to have a branch of the same name.
func resolveGitURLAndRef(r runner.CommandRunner, mod, ref string) (gitURL string, output []byte, err error) {
	parts := strings.Split(mod, "/")
	var lastErr error
	for i := len(parts); i >= 2; i-- {
		url := "https://" + strings.Join(parts[:i], "/")
		proc, runErr := runner.Cmd("git", "ls-remote", url, ref).WithQuiet().Run(r)
		if runErr != nil {
			lastErr = fmt.Errorf("git ls-remote %s failed: %w", url, runErr)
			continue
		}
		out, _ := io.ReadAll(proc.Stdout())
		if waitErr := proc.Wait(); waitErr != nil {
			lastErr = fmt.Errorf("git ls-remote %s failed: %w", url, waitErr)
			continue
		}
		return url, out, nil
	}
	return "", nil, lastErr
}

// resolveVersionViaGit fetches the commit ref points at (a branch, "HEAD",
// or any other ref git ls-remote accepts) and constructs a proper
// pseudo-version with the correct timestamp.
func resolveVersionViaGit(r runner.CommandRunner, mod, ref string) (string, error) {
	gitURL, output, err := resolveGitURLAndRef(r, mod, ref)
	if err != nil {
		return "", fmt.Errorf("git ls-remote failed: %w", err)
	}

	fields := strings.Fields(string(output))
	if len(fields) < 1 {
		return "", fmt.Errorf("no ref %q found for %s", ref, mod)
	}
	fullHash := fields[0]
	if len(fullHash) < 12 {
		return "", fmt.Errorf("invalid commit hash: %s", fullHash)
	}
	shortHash := fullHash[:12]

	// Shallow fetch just the commit to get its timestamp
	tmpDir, err := os.MkdirTemp("", "resolve-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	// Init bare repo and fetch just the one commit
	proc, err := runner.Cmd("git", "-C", tmpDir, "init", "--bare").WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git init failed: %w", err)
	}
	if waitErr := proc.Wait(); waitErr != nil {
		return "", fmt.Errorf("git init failed: %w", waitErr)
	}

	proc, err = runner.Cmd("git", "-C", tmpDir, "fetch", "--depth=1", gitURL, fullHash).WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git fetch failed: %w", err)
	}
	if waitErr := proc.Wait(); waitErr != nil {
		return "", fmt.Errorf("git fetch failed: %w", waitErr)
	}

	// Get commit timestamp in UTC (use Unix epoch and convert)
	proc, err = runner.Cmd("git", "-C", tmpDir, "log", "-1", "--format=%ct", fullHash).WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git log failed: %w", err)
	}
	tsOutput, _ := io.ReadAll(proc.Stdout())
	if waitErr := proc.Wait(); waitErr != nil {
		return "", fmt.Errorf("git log failed: %w", waitErr)
	}

	epochStr := strings.TrimSpace(string(tsOutput))
	epoch, err := strconv.ParseInt(epochStr, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid timestamp: %s", epochStr)
	}
	timestamp := time.Unix(epoch, 0).UTC().Format("20060102150405")

	return fmt.Sprintf("v0.0.0-%s-%s", timestamp, shortHash), nil
}
