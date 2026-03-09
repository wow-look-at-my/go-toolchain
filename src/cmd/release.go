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
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

var (
	releaseTag   string
	releaseFrom  string
	releaseBuild bool
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
	rootCmd.AddCommand(cmd)
}

func runReleaseCmd(cmd *cobra.Command, args []string) error {
	return runReleaseCmdWithStdin(os.Stdin)
}

func runReleaseCmdWithStdin(stdin io.Reader) error {
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
		out, err := exec.Command("git", "describe", "--tags", "--always").Output()
		if err != nil {
			return fmt.Errorf("failed to determine tag (use --tag to specify): %w", err)
		}
		tag = strings.TrimSpace(string(out))
	}

	// Resolve from ref (previous tag)
	from := releaseFrom
	if from == "" {
		out, err := exec.Command("git", "describe", "--tags", "--abbrev=0", "HEAD^").Output()
		if err == nil {
			from = strings.TrimSpace(string(out))
		}
		// If no previous tag exists, from stays empty (first release)
	}

	// Collect commits
	commits, err := collectCommits(from)
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
	notes := generateReleaseNotes(tag, commits, checksumsContent)

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
	if err := gitExec("tag", tag); err != nil {
		return fmt.Errorf("failed to create tag %s: %w", tag, err)
	}
	if err := gitExec("push", "origin", tag); err != nil {
		return fmt.Errorf("failed to push tag %s: %w", tag, err)
	}

	// Update rolling tags
	if err := gitExec("tag", "-f", "master", "HEAD"); err != nil {
		return fmt.Errorf("failed to update master tag: %w", err)
	}
	if err := gitExec("tag", "-f", "latest", "HEAD"); err != nil {
		return fmt.Errorf("failed to update latest tag: %w", err)
	}
	if err := gitExec("push", "-f", "origin", "master", "latest"); err != nil {
		return fmt.Errorf("failed to push rolling tags: %w", err)
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

	// Build gh release create command
	ghArgs := []string{"release", "create", tag}

	// Add binary artifacts
	binaries, _ := filepath.Glob(filepath.Join(outputDir, "go-toolchain_*"))
	for _, b := range binaries {
		// Skip symlinks (host, bare name)
		if info, err := os.Lstat(b); err == nil && info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		ghArgs = append(ghArgs, b)
	}

	// Add checksums and signature files
	for _, name := range []string{"checksums.txt", "checksums.txt.sig", "checksums.txt.pem"} {
		p := filepath.Join(outputDir, name)
		if _, err := os.Stat(p); err == nil {
			ghArgs = append(ghArgs, p)
		}
	}

	ghArgs = append(ghArgs, "--notes-file", notesFile.Name())

	fmt.Printf("==> Creating GitHub release %s\n", tag)
	ghCmd := exec.Command("gh", ghArgs...)
	ghCmd.Stdout = os.Stdout
	ghCmd.Stderr = os.Stderr
	if err := ghCmd.Run(); err != nil {
		return fmt.Errorf("gh release create failed: %w", err)
	}

	fmt.Printf("==> Release %s created successfully\n", tag)
	return nil
}

// collectCommits returns commit subjects between from and HEAD.
// If from is empty, returns all commits (first release).
func collectCommits(from string) ([]string, error) {
	var args []string
	if from != "" {
		args = []string{"log", "--oneline", "--no-decorate", from + "..HEAD"}
	} else {
		args = []string{"log", "--oneline", "--no-decorate"}
	}

	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("failed to collect commits: %w", err)
	}

	var commits []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
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
	return commits, nil
}

// generateReleaseNotes produces markdown release notes.
func generateReleaseNotes(tag string, commits []string, checksumsContent string) string {
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

	sb.WriteString("\n## Verification\n\n```bash\n")
	sb.WriteString("cosign verify-blob checksums.txt \\\n")
	sb.WriteString("  --signature checksums.txt.sig \\\n")
	sb.WriteString("  --certificate checksums.txt.pem \\\n")
	sb.WriteString("  --certificate-oidc-issuer https://token.actions.githubusercontent.com \\\n")
	sb.WriteString("  --certificate-identity-regexp 'github\\.com/wow-look-at-my/go-toolchain'\n")
	sb.WriteString("```\n")

	return sb.String()
}

// gitExec runs a git command and returns any error.
func gitExec(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
