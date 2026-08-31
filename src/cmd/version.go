package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// buildVersion is derived from Go's built-in VCS stamping.
var buildVersion = resolvedVersion()

// vcsInfo reads Go's built-in VCS stamping from the binary.
type vcsInfo struct {
	Revision string
	Time     string
	Modified bool
}

var cachedVCS *vcsInfo

func getVCS() vcsInfo {
	if cachedVCS != nil {
		return *cachedVCS
	}
	var v vcsInfo
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				v.Revision = s.Value
			case "vcs.time":
				v.Time = s.Value
			case "vcs.modified":
				v.Modified = s.Value == "true"
			}
		}
	}
	cachedVCS = &v
	return v
}

func resolvedVersion() string {
	vcs := getVCS()
	if vcs.Time != "" {
		if t, err := time.Parse(time.RFC3339, vcs.Time); err == nil {
			return fmt.Sprintf("v0.0.%d", t.Unix())
		}
	}
	return "dev"
}

func resolvedCommit() string {
	if vcs := getVCS(); vcs.Revision != "" {
		return vcs.Revision
	}
	return "unknown"
}

func resolvedTimestamp() (int64, bool) {
	if vcs := getVCS(); vcs.Time != "" {
		if t, err := time.Parse(time.RFC3339, vcs.Time); err == nil {
			return t.Unix(), true
		}
	}
	return 0, false
}

var githubRepo = envOr("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")

// Overrides where staleness-footer commit queries go; point it unreachable for an instant offline footer in tests.
var githubAPIBase = envOr("GO_TOOLCHAIN_GITHUB_API_URL", "https://api.github.com")

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
	versionCmd.AddCommand(&cobra.Command{
		Use:   "raw",
		Short: "Print just the version number",
		Run:   func(cmd *cobra.Command, args []string) { logger.Output("%s", resolvedVersion()) },
	})
	versionCmd.AddCommand(&cobra.Command{
		Use:   "json",
		Short: "Print version info as JSON",
		Run:   runVersionJSON,
	})
	// The APE runs on several hosts, so "which host is this on" has a
	// fallback answer that can be wrong. Printing the evidence makes it
	// auditable anywhere the binary runs, including inside a sandbox.
	versionCmd.AddCommand(&cobra.Command{
		Use:   "host",
		Short: "Print the detected host OS and the evidence for it",
		Run: func(cmd *cobra.Command, args []string) {
			d := hostos.Detect()
			logger.Output("%s", d)
			logger.Output("goos: %s, goarch: %s", runtime.GOOS, runtime.GOARCH)
		},
	})
	var requireRelease bool
	// Every host in a CI run must build against the same compiler. This
	// prints the release to hand them through GO_TOOLCHAIN_COSMO_VERSION.
	cosmoCmd := &cobra.Command{
		Use:   "cosmo",
		Short: "Print the gosmopolitan release this host would build against",
		RunE:  func(cmd *cobra.Command, args []string) error { return runVersionCosmo(requireRelease) },
	}
	cosmoCmd.Flags().BoolVar(&requireRelease, "require-release", false, "Fail if the resolved version is not a real numbered buildhost release")
	versionCmd.AddCommand(cosmoCmd)
	rootCmd.AddCommand(versionCmd)
}

// runVersionCosmo prints the resolved gosmopolitan version and, with
// requireRelease, fails when it is a branch-key fallback rather than a real
// numbered release -- CI's guarantee that every host resolves the same
// compiler (docs/CI.md).
func runVersionCosmo(requireRelease bool) error {
	v := ResolveCosmoVersion()
	logger.Output("%s", v)
	if requireRelease && !cosmoReleasePattern.MatchString(v) {
		// rawStderr, not logger.Error: the caller captures stdout as the version value.
		fmt.Fprintf(rawStderr, "::error::buildhost did not name a gosmopolitan release (got %q), so each host would resolve its own\n", v)
		return fmt.Errorf("resolved version %q is not a real gosmopolitan release", v)
	}
	return nil
}

type versionOutput struct {
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	CommitDate    string `json:"commit_date,omitempty"`
	BuildDate     string `json:"build_date,omitempty"`
	LatestCommit  string `json:"latest_commit,omitempty"`
	CommitsBehind *int   `json:"commits_behind,omitempty"`
}

func runVersionJSON(cmd *cobra.Command, args []string) {
	commit := resolvedCommit()
	out := versionOutput{
		Version: resolvedVersion(),
		Commit:  commit,
	}

	if ts, ok := resolvedTimestamp(); ok {
		out.CommitDate = time.Unix(ts, 0).UTC().Format(time.RFC3339)

		if commit != "unknown" {
			if latest, err := fetchLatestCommitFromGitHub(); err == nil {
				out.LatestCommit = latest.sha
				behind := 0
				if latest.timestamp > ts {
					if count, err := fetchCommitsBehind(commit, latest.sha); err == nil {
						behind = count
					}
				}
				out.CommitsBehind = &behind
			}
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(out) //nolint:errcheck
}

func runVersion(cmd *cobra.Command, args []string) {
	printVersionInfo()
	printStaleness()
}

func printVersionInfo() {
	logger.Output("Version:     %s", resolvedVersion())
	logger.Output("Commit:      %s", resolvedCommit())

	if ts, ok := resolvedTimestamp(); ok {
		logger.Output("Commit date: %s", time.Unix(ts, 0).UTC().Format(time.RFC3339))
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
	builtTs, ok := resolvedTimestamp()
	commit := resolvedCommit()
	if !ok || commit == "unknown" {
		logger.Output("\nNo build info embedded (dev build).")
		return
	}

	latest, err := fetchLatestCommitFromGitHub()
	if err != nil {
		logger.Output("\nCould not check for updates: %v", err)
		return
	}

	if latest.timestamp <= builtTs {
		logger.Output("\nBuild is up to date with latest commit.")
		return
	}

	diff := time.Duration(latest.timestamp-builtTs) * time.Second
	msg := fmt.Sprintf("\nBuild is %s behind latest commit.", formatDuration(diff))

	if count, err := fetchCommitsBehind(commit, latest.sha); err == nil && count > 0 {
		msg += fmt.Sprintf(" (%d commits)", count)
	}

	logger.Output("%s", msg)
}

// newGitHubRequest creates an HTTP GET request, adding an Authorization
// header if a GitHub token is discovered in the environment. This raises
// the hourly rate limit from the anonymous allowance to the authenticated allowance.
func newGitHubRequest(url string) (*http.Request, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	if token := discoverGitHubToken(); token != "" {
		req.Header.Set("Authorization", "token "+token)
	}
	return req, nil
}

func fetchLatestCommitFromGitHub() (*commitInfo, error) {
	url := fmt.Sprintf("%s/repos/%s/commits?per_page=1", githubAPIBase, githubRepo)
	req, err := newGitHubRequest(url)
	if err != nil {
		return nil, fmt.Errorf("GitHub API request failed: %w", err)
	}
	resp, err := httpClient.Do(req)
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
	req, err := newGitHubRequest(url)
	if err != nil {
		return 0, err
	}
	resp, err := httpClient.Do(req)
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
