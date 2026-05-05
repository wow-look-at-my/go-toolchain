package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-containers/set"
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
		Short:        "Create a GitHub release with checksums and structured notes",
		Long:         "Orchestrates the full release lifecycle: optional build, release notes generation, tag management, and GitHub release creation.",
		SilenceUsage: true,
		RunE:         runReleaseCmd,
	}
	cmd.Flags().StringVar(&releaseTag, "tag", "", "Tag name for this release (required in CI, default: auto-generated)")
	cmd.Flags().StringVar(&releaseFrom, "from", "", "Start ref for changelog (default: previous tag)")
	cmd.Flags().BoolVar(&releaseBuild, "build", false, "Run matrix cross-compilation before releasing")
	cmd.Flags().BoolVar(&releaseCosign, "cosign", false, "Include cosign signature files and verification section (default: auto, enabled on github.com)")
	cmd.Flags().BoolVar(&releaseNoCosign, "no-cosign", false, "Skip cosign signature files and verification section in release notes")
	rootCmd.AddCommand(cmd)
}

// resolveNoCosign returns true if cosign should be skipped, based on explicit
// flags and the server URL for auto-detection. serverURL is GITHUB_SERVER_URL.
func resolveNoCosign(cosign, noCosign bool, serverURL string) (bool, error) {
	if cosign && noCosign {
		return false, fmt.Errorf("--cosign and --no-cosign are mutually exclusive")
	}
	if cosign {
		return false, nil
	}
	if noCosign {
		return true, nil
	}
	// Auto: enable cosign only when running on github.com
	return !strings.Contains(serverURL, "github.com"), nil
}

// releaseExecutor abstracts external command execution for testability.
type releaseExecutor interface {
	// gitOutput runs a git command and returns its stdout.
	gitOutput(args ...string) (string, error)
	// gitRun runs a git command, connecting stdout/stderr to the terminal.
	gitRun(args ...string) error
	// ghRelease runs gh release create with the given arguments.
	ghRelease(args ...string) error
}

// realExecutor shells out to git/gh.
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

func (realExecutor) ghRelease(args ...string) error {
	cmd := exec.Command("gh", append([]string{"release"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runReleaseCmd(cmd *cobra.Command, args []string) error {
	noCosign, err := resolveNoCosign(releaseCosign, releaseNoCosign, os.Getenv("GITHUB_SERVER_URL"))
	if err != nil {
		return err
	}
	return runReleaseCmdImpl(os.Stdin, realExecutor{}, noCosign)
}

func runReleaseCmdImpl(stdin io.Reader, ex releaseExecutor, noCosign bool) error {
	// Optional: run matrix build first
	if releaseBuild {
		r := runner.New()
		if err := runReleaseWithRunner(r); err != nil {
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
		// If no previous tag exists, from stays empty (first release)
	}

	// Collect commits
	commits, err := collectCommitsWithExecutor(from, ex)
	if err != nil {
		return err
	}

	// Read checksums if available
	checksumsContent := ""
	checksumsPath := filepath.Join(outputDir, "checksums.txt")
	if data, err := os.ReadFile(checksumsPath); err == nil {
		checksumsContent = string(data)
	}

	// Generate release notes
	notes := generateReleaseNotes(tag, commits, checksumsContent, noCosign)

	// Interactive confirmation when not in CI
	if os.Getenv("CI") == "" {
		fmt.Fprintf(os.Stderr, "Release: %s\n", tag)
		fmt.Fprintf(os.Stderr, "Commits: %d\n", len(commits))
		fmt.Fprintf(os.Stderr, "\n--- Release Notes ---\n%s\n---------------------\n\n", notes)
		fmt.Fprintf(os.Stderr, "Are you sure you want to create this release? [y/N] ")

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

	// Update the rolling "latest" tag to point at this release. Push with the
	// explicit refs/tags/ prefix so the refspec is unambiguous even if the
	// consumer's repo happens to have a branch of the same name.
	if err := ex.gitRun("tag", "-f", "latest", "HEAD"); err != nil {
		return fmt.Errorf("failed to update latest tag: %w", err)
	}
	if err := ex.gitRun("push", "-f", "origin", "refs/tags/latest"); err != nil {
		return fmt.Errorf("failed to push latest tag: %w", err)
	}

	// Write release notes to temp file for gh
	notesFile, err := os.CreateTemp("", "release-notes-*.md")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(notesFile.Name())
	if _, err := notesFile.WriteString(notes); err != nil {
		notesFile.Close()
		return fmt.Errorf("failed to write release notes: %w", err)
	}
	notesFile.Close()

	// Build gh release create args
	ghArgs := []string{"create", tag}

	// Build the set of files to include in the release. checksums.txt is
	// the authoritative list of platform binaries produced by `matrix` —
	// the bare host-name aliases (`<name>` and `<name>_host`) are
	// deliberately excluded from it. Filtering against this allowlist
	// keeps the release clean even when the build directory has been
	// roundtripped through actions/upload-artifact (which dereferences
	// symlinks, turning the aliases into full duplicate file copies that
	// the symlink-mode check can no longer recognize).
	expected := set.Of("checksums.txt")
	if !noCosign {
		expected.Add("checksums.txt.sig")
		expected.Add("checksums.txt.pem")
	}
	for _, line := range strings.Split(checksumsContent, "\n") {
		// Format: "<hex-digest>  <filename>"
		if _, name, ok := strings.Cut(strings.TrimSpace(line), "  "); ok {
			expected.Add(name)
		}
	}

	entries, _ := filepath.Glob(filepath.Join(outputDir, "*"))
	for _, p := range entries {
		info, err := os.Lstat(p)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !expected.Contains(filepath.Base(p)) {
			continue
		}
		ghArgs = append(ghArgs, p)
	}

	ghArgs = append(ghArgs, "--notes-file", notesFile.Name())

	fmt.Printf("⇒ Creating GitHub release %s\n", tag)
	if err := ex.ghRelease(ghArgs...); err != nil {
		return fmt.Errorf("gh release create failed: %w", err)
	}

	fmt.Printf("⇒ Release %s created successfully\n", tag)
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

// generateReleaseNotes produces markdown release notes.
func generateReleaseNotes(tag string, commits []string, checksumsContent string, noCosign bool) string {
	var sb strings.Builder

	sb.WriteString("## What's Changed\n\n")
	if len(commits) == 0 {
		sb.WriteString("- Initial release\n")
	} else {
		for _, c := range commits {
			fmt.Fprintf(&sb, "- %s\n", c)
		}
	}

	if checksumsContent != "" {
		sb.WriteString("\n## Checksums\n\n```\n")
		sb.WriteString(checksumsContent)
		sb.WriteString("```\n")
	}

	if !noCosign {
		sb.WriteString("\n## Verification\n\n```bash\n")
		sb.WriteString("cosign verify-blob checksums.txt \\\n")
		sb.WriteString("  --signature checksums.txt.sig \\\n")
		sb.WriteString("  --certificate checksums.txt.pem \\\n")
		sb.WriteString("  --certificate-oidc-issuer https://token.actions.githubusercontent.com \\\n")
		sb.WriteString("  --certificate-identity-regexp 'github\\.com/wow-look-at-my/go-toolchain'\n")
		sb.WriteString("```\n")
	}

	return sb.String()
}
