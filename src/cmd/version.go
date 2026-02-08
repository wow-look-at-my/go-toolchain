package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

var githubRepo = envOr("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")
var githubAPIBase = "https://api.github.com"

func setGithubRepo(repo string)    { githubRepo = repo }
func setGithubAPIBase(base string) { githubAPIBase = base }

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

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

// httpClient is the HTTP client used for GitHub API calls. Replaceable for testing.
var httpClient = &http.Client{Timeout: 10 * time.Second}

// commitInfo holds metadata about a git commit.
type commitInfo struct {
	sha       string
	timestamp int64
}

func printStaleness() {
	if buildTimestamp == "" || buildCommit == "unknown" {
		fmt.Println("\nNo build info embedded (dev build).")
		return
	}

	builtTs, err := strconv.ParseInt(buildTimestamp, 10, 64)
	if err != nil {
		return
	}

	latest, err := fetchLatestCommitFromGitHub()
	if err != nil {
		fmt.Printf("\nCould not check for updates: %v\n", err)
		return
	}

	if latest.timestamp <= builtTs {
		fmt.Println("\nBuild is up to date with latest commit.")
		return
	}

	diff := time.Duration(latest.timestamp-builtTs) * time.Second
	msg := fmt.Sprintf("\nBuild is %s behind latest commit.", formatDuration(diff))

	if count, err := fetchCommitsBehind(buildCommit, latest.sha); err == nil && count > 0 {
		msg += fmt.Sprintf(" (%d commits)", count)
	}

	fmt.Println(msg)
}

func fetchLatestCommitFromGitHub() (*commitInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/commits?per_page=1", githubAPIBase, githubRepo)
	resp, err := httpClient.Get(url)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned HTTP %d", resp.StatusCode)
	}

	var commits []struct {
		SHA    string `json:"sha"`
		Commit struct {
			Committer struct {
				Date time.Time `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&commits); err != nil {
		return nil, fmt.Errorf("failed to parse GitHub response: %w", err)
	}

	if len(commits) == 0 {
		return nil, fmt.Errorf("no commits found in repository")
	}

	return &commitInfo{
		sha:       commits[0].SHA,
		timestamp: commits[0].Commit.Committer.Date.Unix(),
	}, nil
}

func fetchCommitsBehind(fromCommit, toCommit string) (int, error) {
	url := fmt.Sprintf("%s/repos/%s/compare/%s...%s", githubAPIBase, githubRepo, fromCommit, toCommit)
	resp, err := httpClient.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var result struct {
		AheadBy int `json:"ahead_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}

	return result.AheadBy, nil
}

// gitInfo holds version metadata collected from git.
type gitInfo struct {
	version   string
	commit    string
	timestamp string
}

// collectGitInfo gathers version metadata from environment variables first
// (GITHUB_SHA, GITHUB_REF_NAME, GITHUB_REF_TYPE), falling back to git
// commands only when env vars are not set.
func collectGitInfo() gitInfo {
	var info gitInfo

	// Commit: GITHUB_SHA or git rev-parse HEAD
	info.commit = os.Getenv("GITHUB_SHA")
	if info.commit == "" {
		if out, err := exec.Command("git", "rev-parse", "HEAD").Output(); err == nil {
			info.commit = strings.TrimSpace(string(out))
		}
	}

	// Version: use tag ref from CI, or git describe
	if os.Getenv("GITHUB_REF_TYPE") == "tag" {
		info.version = os.Getenv("GITHUB_REF_NAME")
	}
	if info.version == "" {
		if out, err := exec.Command("git", "describe", "--tags", "--always", "--dirty").Output(); err == nil {
			info.version = strings.TrimSpace(string(out))
		}
	}

	// Timestamp: no env var for this, always from git
	if out, err := exec.Command("git", "log", "-1", "--format=%ct").Output(); err == nil {
		info.timestamp = strings.TrimSpace(string(out))
	}

	return info
}

// ldflags returns a string suitable for `go build -ldflags`.
// Build date uses SOURCE_DATE_EPOCH or the git commit timestamp
// for reproducible builds (never wall-clock time).
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
	// Build date: SOURCE_DATE_EPOCH > git commit timestamp
	if epoch := os.Getenv("SOURCE_DATE_EPOCH"); epoch != "" {
		if ts, err := strconv.ParseInt(epoch, 10, 64); err == nil {
			flags = append(flags, fmt.Sprintf("-X %s.buildDate=%s", ldflagsPrefix, time.Unix(ts, 0).UTC().Format(time.RFC3339)))
		}
	} else if g.timestamp != "" {
		if ts, err := strconv.ParseInt(g.timestamp, 10, 64); err == nil {
			flags = append(flags, fmt.Sprintf("-X %s.buildDate=%s", ldflagsPrefix, time.Unix(ts, 0).UTC().Format(time.RFC3339)))
		}
	}
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
