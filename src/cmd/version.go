package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/memlimit"
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
	rootCmd.AddCommand(versionCmd)
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

// checkDirtyInCI returns an error if running in CI with a dirty working
// tree, so binaries are never shipped built from uncommitted changes.
//
// Excluded: the transient GOMEMLIMIT guard in any state (added, modified,
// deleted -- it is generated for the build and removed after, so it must
// never count), and a branch-tracked pin's version token moving to its
// branch's current commit (a cache of the last resolution, not something a
// human commits). Anything else in go.mod, and any go.sum line for a module
// that did not move, still counts as dirty.
func checkDirtyInCI() error {
	if os.Getenv("CI") == "" {
		return nil
	}
	out, err := exec.Command("git", "status", "--short").Output()
	if err != nil {
		return nil
	}
	files := dirtyFilesExcludingToolchainWrites(string(out))
	if files == "" {
		return nil
	}
	if !jsonOutput {
		logError("", fmt.Sprintf(
			"Working tree is dirty in CI (go-toolchain %s). Dirty files:\n%s\n%s\n"+
				"Fix: run `go-toolchain` locally, review the diff, commit the changes, and push.",
			buildVersion, files, dirtyDiff(files)))
	}
	return fmt.Errorf("working tree is dirty in CI (run `go-toolchain` locally, review the diff, commit, and push)")
}

// dirtyDiff renders what actually changed. The message tells the reader to
// review the diff, and when the failure happens only in CI there is no other
// way to see it: the tree is gone with the runner.
func dirtyDiff(files string) string {
	paths := dirtyDiffPaths(files)
	if len(paths) == 0 {
		return ""
	}
	out, err := exec.Command("git", append([]string{"--no-pager", "diff", "--"}, paths...)...).Output()
	if err != nil {
		return ""
	}
	diff := strings.TrimSpace(string(out))
	if diff == "" {
		return "" // untracked only, so git has nothing to compare against
	}
	if lines := strings.Split(diff, "\n"); len(lines) > dirtyDiffMaxLines {
		diff = strings.Join(lines[:dirtyDiffMaxLines], "\n") + "\n... diff truncated"
	}
	return "\nDiff:\n" + diff + "\n"
}

// dirtyDiffMaxLines keeps a runaway diff from burying the message above it.
const dirtyDiffMaxLines = 200

// dirtyDiffPaths reads the paths out of `git status --short` lines. The status
// code sits in front and a rename carries an arrow, so the path is the last
// field either way.
func dirtyDiffPaths(files string) []string {
	var paths []string
	for line := range strings.SplitSeq(files, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 {
			paths = append(paths, fields[len(fields)-1])
		}
	}
	return paths
}

// dirtyFilesExcludingToolchainWrites returns the trimmed `git status --short`
// lines that represent real uncommitted changes, dropping the GOMEMLIMIT
// guard and a branch-tracked pin following its branch. An empty result means
// the tree is clean apart from those.
func dirtyFilesExcludingToolchainWrites(statusOut string) string {
	pins := trackedPinMoves(statusOut)
	var kept []string
	for _, line := range strings.Split(statusOut, "\n") {
		if strings.TrimSpace(line) == "" || statusLineIsToolchainWrite(line, pins) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// statusLineIsToolchainWrite reports whether a `git status --short` porcelain
// line refers to a file this run rewrote on its own authority. The format is
// "XY <path>" (or "XY <old> -> <new>" for renames); the GOMEMLIMIT guard is
// matched by base name so it is ignored in any package directory, while the
// go.mod and go.sum cases are decided per module directory from pins.
func statusLineIsToolchainWrite(line string, pins map[string][]string) bool {
	path := statusLinePath(line)
	if path == "" {
		return false
	}
	if filepath.Base(path) == memlimit.GuardFileName {
		return true
	}
	// A tracked pin's new commit, and the go.sum hashes that follow it. pins
	// holds a directory only when its go.mod changed in no other way.
	if moved, ok := pins[filepath.Dir(path)]; ok {
		switch filepath.Base(path) {
		case "go.mod":
			return true
		case "go.sum":
			return goSumFollowsPins(path, moved)
		}
	}
	// A .gitignore diff that only drops the stale guard line is toolchain migration cleanup, not a developer edit.
	if filepath.Base(path) == ".gitignore" && gitignoreChangeOnlyDropsGuard(path) {
		return true
	}
	return false
}

// statusLinePath extracts the path from a `git status --short` line ("XY
// <path>", or a rename's new name). Returns "" for a line too short to carry a path.
func statusLinePath(line string) string {
	if len(line) < 4 {
		return ""
	}
	path := strings.TrimSpace(line[3:])
	if i := strings.Index(path, " -> "); i != -1 {
		path = path[i+len(" -> "):]
	}
	return strings.Trim(path, "\"")
}

// gitignoreChangeOnlyDropsGuard reports whether the working-tree change to the
// .gitignore at path, relative to HEAD, is solely the removal of the GOMEMLIMIT
// guard line.
func gitignoreChangeOnlyDropsGuard(path string) bool {
	out, err := exec.Command("git", "diff", "HEAD", "--", path).Output()
	if err != nil {
		return false
	}
	return diffOnlyDropsGuard(string(out))
}

// diffOnlyDropsGuard parses a unified diff and reports whether every content
// change is the removal of the guard line: a guard line removed, no
// additions, and nothing else removed (blank-line churn aside). It is split out
// from the git invocation so it can be unit-tested without a repository.
func diffOnlyDropsGuard(diff string) bool {
	sawGuardRemoval := false
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
			// file headers, not content
		case strings.HasPrefix(line, "+"):
			if strings.TrimSpace(line[1:]) != "" {
				return false // a real addition
			}
		case strings.HasPrefix(line, "-"):
			content := strings.TrimSpace(line[1:])
			if content == "" {
				continue // removed blank line: cosmetic
			}
			if content != memlimit.GuardFileName {
				return false // removed something other than the guard
			}
			sawGuardRemoval = true
		}
	}
	return sawGuardRemoval
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
