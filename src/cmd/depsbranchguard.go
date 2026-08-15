package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// A branch that is the head of an open pull request is a branch with a
// scheduled death: the merge that closes the PR deletes it. Tracking one
// resolves fine until that moment and then resolves to nothing, on the
// DEFAULT branch, after the change that broke it has already landed.
//
// So a named branch is checked against the open pull requests of the repo it
// belongs to. In CI this FAILS, because CI is the last look at the change
// before it merges and green there is what the merge is decided on. Locally it
// only warns: developing two repos in tandem, pointed at each other's
// unmerged branches, is a real workflow, and the warning is the reminder to
// repoint before the pull request goes up.
//
// A bare auto-branch marker is never checked: it names no branch, so it
// follows whatever the default branch is and cannot be pointed at a temporary
// one.

// temporaryBranch is a tracked branch that an open pull request will delete.
type temporaryBranch struct {
	module string
	branch string
	pr     string
}

func (t temporaryBranch) String() string {
	return fmt.Sprintf("%s follows branch %q, which is the head of %s and is deleted when that merges", t.module, t.branch, t.pr)
}

// reportTemporaryBranches fails the run in CI and warns outside it. A run with
// nothing to report costs nothing.
func reportTemporaryBranches(found []temporaryBranch) error {
	if len(found) == 0 {
		return nil
	}
	var lines []string
	for _, t := range found {
		lines = append(lines, "  "+t.String())
	}
	detail := strings.Join(lines, "\n")
	if os.Getenv("CI") == "" {
		logger.Warn("tracking a branch with an open pull request:\n%s\nRepoint these at the default branch (%s, no value) before the change merges.", detail, autoBranchMarker)
		return nil
	}
	return fmt.Errorf("tracking a branch with an open pull request:\n%s\nThe branch is deleted when that pull request merges, so this resolves today and stops resolving right after the merge. Repoint at the default branch (%s, no value), or wait for the dependency to merge and re-run", detail, autoBranchMarker)
}

// checkTemporaryBranch reports whether branch is the head of an open pull
// request in the repository mod belongs to.
//
// It answers "cannot tell" as no finding, with a warning. A guard that turned
// an unreachable API into a failed build would fail runs over the network
// rather than over the thing it checks -- but a guard that said nothing at all
// would read exactly like a clean result.
func checkTemporaryBranch(mod, branch string) (temporaryBranch, bool) {
	owner, repo, ok := gitHubOwnerRepo(mod)
	if !ok {
		return temporaryBranch{}, false // only GitHub has an API to ask
	}

	api := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?state=open&per_page=1&head=%s",
		owner, repo, url.QueryEscape(owner+":"+branch))
	req, err := http.NewRequest(http.MethodGet, api, nil)
	if err != nil {
		return temporaryBranch{}, false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := gitHubAPIToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		logger.Warn("could not check whether %s/%s branch %q has an open pull request: %v", owner, repo, branch, err)
		return temporaryBranch{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logger.Warn("could not check whether %s/%s branch %q has an open pull request: HTTP %d (a private repository needs GITHUB_TOKEN or GH_TOKEN in the environment)", owner, repo, branch, resp.StatusCode)
		return temporaryBranch{}, false
	}

	var open []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&open); err != nil {
		logger.Warn("could not read the open pull requests of %s/%s: %v", owner, repo, err)
		return temporaryBranch{}, false
	}
	if len(open) == 0 {
		return temporaryBranch{}, false
	}
	pr := open[0].HTMLURL
	if pr == "" {
		pr = fmt.Sprintf("%s/%s#%d", owner, repo, open[0].Number)
	}
	return temporaryBranch{branch: branch, pr: pr}, true
}

// gitHubOwnerRepo reads the owner and repository out of a github.com module
// path. On GitHub the repository is always the first three segments, so a
// module in a subdirectory answers this without a lookup.
func gitHubOwnerRepo(mod string) (owner, repo string, ok bool) {
	rest, found := strings.CutPrefix(mod, "github.com/")
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// gitHubAPIToken returns the token the GitHub API is asked with, if the
// environment carries one. GITHUB_TOKEN is what Actions provides.
func gitHubAPIToken() string {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := os.Getenv(name); v != "" {
			return v
		}
	}
	return ""
}
