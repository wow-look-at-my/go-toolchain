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

// resolveLatestVersionViaGit fetches the latest commit from a git repo and
// constructs a proper pseudo-version with the correct timestamp.
func resolveLatestVersionViaGit(r runner.CommandRunner, mod string) (string, error) {
	gitURL := "https://" + mod

	// Get HEAD commit hash via ls-remote
	proc, err := runner.Cmd("git", "ls-remote", gitURL, "HEAD").WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git ls-remote failed: %w", err)
	}
	output, _ := io.ReadAll(proc.Stdout())
	if proc.Wait() != nil {
		return "", fmt.Errorf("git ls-remote failed: %w", err)
	}

	fields := strings.Fields(string(output))
	if len(fields) < 1 {
		return "", fmt.Errorf("no HEAD ref found")
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
	proc, err = runner.Cmd("git", "-C", tmpDir, "init", "--bare").WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git init failed: %w", err)
	}
	if proc.Wait() != nil {
		return "", fmt.Errorf("git init failed: %w", err)
	}

	proc, err = runner.Cmd("git", "-C", tmpDir, "fetch", "--depth=1", gitURL, fullHash).WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git fetch failed: %w", err)
	}
	if proc.Wait() != nil {
		return "", fmt.Errorf("git fetch failed: %w", err)
	}

	// Get commit timestamp in UTC (use Unix epoch and convert)
	proc, err = runner.Cmd("git", "-C", tmpDir, "log", "-1", "--format=%ct", fullHash).WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git log failed: %w", err)
	}
	tsOutput, _ := io.ReadAll(proc.Stdout())
	if proc.Wait() != nil {
		return "", fmt.Errorf("git log failed: %w", err)
	}

	epochStr := strings.TrimSpace(string(tsOutput))
	epoch, err := strconv.ParseInt(epochStr, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid timestamp: %s", epochStr)
	}
	timestamp := time.Unix(epoch, 0).UTC().Format("20060102150405")

	return fmt.Sprintf("v0.0.0-%s-%s", timestamp, shortHash), nil
}
