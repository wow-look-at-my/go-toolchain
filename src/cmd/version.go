package cmd

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Build-time variables, set via -ldflags -X.
var (
	buildVersion   = "dev"
	buildCommit    = "unknown"
	buildTimestamp = ""
	buildDate      = ""
)

const ldflagsPrefix = "github.com/wow-look-at-my/go-toolchain/src/cmd"

func init() {
	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Show build version and staleness information",
		Run:   runVersion,
	}
	rootCmd.AddCommand(versionCmd)
}

func runVersion(cmd *cobra.Command, args []string) {
	printVersionInfo()
	printStaleness()
}

func printVersionInfo() {
	fmt.Printf("Version:     %s\n", buildVersion)
	fmt.Printf("Commit:      %s\n", buildCommit)

	if buildTimestamp != "" {
		if ts, err := strconv.ParseInt(buildTimestamp, 10, 64); err == nil {
			commitTime := time.Unix(ts, 0).UTC()
			fmt.Printf("Commit date: %s\n", commitTime.Format(time.RFC3339))
		}
	}

	if buildDate != "" {
		fmt.Printf("Build date:  %s\n", buildDate)
	}
}

func printStaleness() {
	if buildTimestamp == "" || buildCommit == "unknown" {
		return
	}

	builtTs, err := strconv.ParseInt(buildTimestamp, 10, 64)
	if err != nil {
		return
	}

	// Get latest commit timestamp from git
	out, err := exec.Command("git", "log", "-1", "--format=%ct").Output()
	if err != nil {
		return // not in a git repo
	}

	latestTs, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return
	}

	if latestTs <= builtTs {
		fmt.Println("\nBuild is up to date with latest commit.")
		return
	}

	diff := time.Duration(latestTs-builtTs) * time.Second
	fmt.Printf("\nBuild is %s behind latest commit.", formatDuration(diff))

	// Count commits behind
	out, err = exec.Command("git", "rev-list", "--count", buildCommit+"..HEAD").Output()
	if err == nil {
		count := strings.TrimSpace(string(out))
		if count != "0" {
			fmt.Printf(" (%s commits)", count)
		}
	}
	fmt.Println()
}

// gitInfo holds version metadata collected from git.
type gitInfo struct {
	version   string
	commit    string
	timestamp string
}

// collectGitInfo runs git commands to gather version metadata.
func collectGitInfo() gitInfo {
	var info gitInfo
	if out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output(); err == nil {
		info.version = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
		info.commit = strings.TrimSpace(string(out))
	}
	if out, err := exec.Command("git", "log", "-1", "--format=%ct").Output(); err == nil {
		info.timestamp = strings.TrimSpace(string(out))
	}
	return info
}

// ldflags returns a string suitable for `go build -ldflags`.
func (g gitInfo) ldflags() string {
	var flags []string
	if g.version != "" {
		flags = append(flags, fmt.Sprintf("-X %s.buildVersion=%s", ldflagsPrefix, g.version))
	}
	if g.commit != "" {
		flags = append(flags, fmt.Sprintf("-X %s.buildCommit=%s", ldflagsPrefix, g.commit))
	}
	if g.timestamp != "" {
		flags = append(flags, fmt.Sprintf("-X %s.buildTimestamp=%s", ldflagsPrefix, g.timestamp))
	}
	flags = append(flags, fmt.Sprintf("-X %s.buildDate=%s", ldflagsPrefix, time.Now().UTC().Format(time.RFC3339)))
	return strings.Join(flags, " ")
}

func (g gitInfo) String() string {
	if g.version != "" {
		return g.version
	}
	if g.commit != "" {
		if len(g.commit) > 7 {
			return g.commit[:7]
		}
		return g.commit
	}
	return "unknown"
}

func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	if hours < 1 {
		mins := int(d.Minutes())
		if mins <= 1 {
			return "1 minute"
		}
		return fmt.Sprintf("%d minutes", mins)
	}
	if hours == 1 {
		return "1 hour"
	}
	if hours < 24 {
		return fmt.Sprintf("%d hours", hours)
	}
	days := hours / 24
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}
