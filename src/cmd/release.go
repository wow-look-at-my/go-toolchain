package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

var (
	releaseTag      string
	releaseFrom     string
	releaseBuild    bool
	releaseCosign   bool
	releaseNoCosign bool
)

func init() {
	cmd := &cobra.Command{
		Use:          "release",
		Short:        "Tag a release and push the git tags",
		Long:         "Creates and pushes a versioned git tag plus the rolling 'latest' tag.",
		SilenceUsage: true,
		RunE:         runReleaseCmd,
	}
	cmd.Flags().StringVar(&releaseTag, "tag", "", "Tag name for this release (required in CI, default: auto-generated)")
	cmd.Flags().StringVar(&releaseFrom, "from", "", "Start ref for changelog (default: previous tag)")
	cmd.Flags().BoolVar(&releaseBuild, "build", false, "Run matrix cross-compilation before releasing")
	// --build reuses the matrix build, honoring the same target flags (--os/--arch/--targets/--cosmo-platforms).
	addMatrixTargetFlags(cmd)
	cmd.Flags().BoolVar(&releaseCosign, "cosign", false, "Include cosign signature files and verification section (default: auto, enabled on github.com)")
	cmd.Flags().BoolVar(&releaseNoCosign, "no-cosign", false, "Skip cosign signature files and verification section in release notes")
	rootCmd.AddCommand(cmd)
}

// parseRemoteHost extracts the hostname from a git remote URL, supporting
// https://, ssh://, and SCP-style (git@host:) formats. Returns "" if the
// URL doesn't look like a network remote (e.g. local path or empty).
func parseRemoteHost(remoteURL string) string {
	if remoteURL == "" {
		return ""
	}
	// SCP-style: git@host:owner/repo.git
	if !strings.Contains(remoteURL, "://") {
		if at := strings.Index(remoteURL, "@"); at >= 0 {
			hostAndPath := remoteURL[at+1:]
			if colon := strings.Index(hostAndPath, ":"); colon >= 0 {
				return hostAndPath[:colon]
			}
		}
		return "" // local path or unrecognized
	}
	// URL scheme: https://host/... or ssh://host/...
	rest := remoteURL[strings.Index(remoteURL, "://")+3:]
	if slash := strings.Index(rest, "/"); slash >= 0 {
		rest = rest[:slash]
	}
	// Strip userinfo (user@host)
	if at := strings.Index(rest, "@"); at >= 0 {
		rest = rest[at+1:]
	}
	// Strip port
	if colon := strings.LastIndex(rest, ":"); colon >= 0 {
		rest = rest[:colon]
	}
	return rest
}

// resolveNoCosign returns true if cosign should be skipped. Explicit flags
// take priority; otherwise the git remote URL is inspected. If the URL can't
// be parsed to a recognizable host, ghHost (GH_HOST env var) is used as a
// fallback.
func resolveNoCosign(cosign, noCosign bool, remoteURL, ghHost string) (bool, error) {
	if cosign && noCosign {
		return false, fmt.Errorf("--cosign and --no-cosign are mutually exclusive")
	}
	if cosign {
		return false, nil
	}
	if noCosign {
		return true, nil
	}
	// Auto-detect from remote URL.
	if host := parseRemoteHost(remoteURL); host != "" {
		return host != "github.com", nil
	}
	// Fall back to GH_HOST when the remote URL isn't parseable.
	if ghHost != "" {
		return ghHost != "github.com", nil
	}
	return true, nil // default: skip cosign
}

// releaseExecutor abstracts external command execution for testability.
type releaseExecutor interface {
	// gitOutput runs a git command and returns its stdout.
	gitOutput(args ...string) (string, error)
	// gitRun runs a git command, connecting stdout/stderr to the terminal.
	gitRun(args ...string) error
}

// realExecutor shells out to git.
type realExecutor struct{}

func (realExecutor) gitOutput(args ...string) (string, error) {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (realExecutor) gitRun(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runReleaseCmd(cmd *cobra.Command, args []string) error {
	ex := realExecutor{}
	remoteURL, _ := ex.gitOutput("remote", "get-url", "origin")
	noCosign, err := resolveNoCosign(releaseCosign, releaseNoCosign, remoteURL, os.Getenv("GH_HOST"))
	if err != nil {
		return err
	}
	return runReleaseCmdImpl(os.Stdin, ex, noCosign)
}

func runReleaseCmdImpl(stdin io.Reader, ex releaseExecutor, noCosign bool) error {
	// Optional: run matrix build first
	if releaseBuild {
		r := runner.New()
		if err := runReleaseWithRunner(r, nil); err != nil {
			return fmt.Errorf("build failed: %w", err)
		}
	}

	// Resolve tag
	tag := releaseTag
	if tag == "" {
		out, err := ex.gitOutput("describe", "--tags", "--always")
		if err != nil {
			return fmt.Errorf("failed to determine tag (use --tag to specify): %w", err)
		}
		tag = out
	}

	// Resolve from ref (previous tag)
	from := releaseFrom
	if from == "" {
		if out, err := ex.gitOutput("describe", "--tags", "--abbrev=0", "HEAD^"); err == nil {
			from = out
		}
	}

	// Collect commits for display during confirmation
	commits, err := collectCommitsWithExecutor(from, ex)
	if err != nil {
		return err
	}

	// Interactive confirmation when not in CI
	if os.Getenv("CI") == "" {
		// Must reach stderr at any level with no trailing newline; bypasses the logger via rawStderr.
		fmt.Fprintf(rawStderr, "Release: %s\nCommits: %d\n"+
			"Are you sure you want to tag and push %s? [y/N] ", tag, len(commits), tag)

		scanner := bufio.NewScanner(stdin)
		if !scanner.Scan() || !strings.EqualFold(strings.TrimSpace(scanner.Text()), "y") {
			return fmt.Errorf("release aborted")
		}
	}

	// Create and push tag
	if err := ex.gitRun("tag", tag); err != nil {
		return fmt.Errorf("failed to create tag %s: %w", tag, err)
	}
	if err := ex.gitRun("push", "origin", tag); err != nil {
		return fmt.Errorf("failed to push tag %s: %w", tag, err)
	}

	// Update the rolling "latest" tag to point at this release.
	if err := ex.gitRun("tag", "-f", "latest", "HEAD"); err != nil {
		return fmt.Errorf("failed to update latest tag: %w", err)
	}
	if err := ex.gitRun("push", "-f", "origin", "refs/tags/latest"); err != nil {
		return fmt.Errorf("failed to push latest tag: %w", err)
	}

	logger.Output("⇒ Tagged and pushed %s", tag)
	return nil
}

// collectCommitsWithExecutor uses the given executor to run git log.
func collectCommitsWithExecutor(from string, ex releaseExecutor) ([]string, error) {
	var logRange string
	if from != "" {
		logRange = from + "..HEAD"
	}

	var args []string
	if logRange != "" {
		args = []string{"log", "--oneline", "--no-decorate", logRange}
	} else {
		args = []string{"log", "--oneline", "--no-decorate"}
	}

	out, err := ex.gitOutput(args...)
	if err != nil {
		return nil, fmt.Errorf("failed to collect commits: %w", err)
	}

	return parseCommitLines(out), nil
}

// parseCommitLines parses git log --oneline output into commit messages.
func parseCommitLines(output string) []string {
	var commits []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Strip the short hash prefix (everything before first space)
		if idx := strings.IndexByte(line, ' '); idx >= 0 {
			line = line[idx+1:]
		}
		commits = append(commits, line)
	}
	return commits
}
