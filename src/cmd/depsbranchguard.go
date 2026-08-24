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

// temporaryBranch is a tracked branch that an open pull request will delete.
type temporaryBranch struct {
	module string
	branch string
	pr     string
}

func (t temporaryBranch) String() string {
	return fmt.Sprintf("%s follows branch %q, which is the head of %s and is deleted when that merges", t.module, t.branch, t.pr)
}

// reportUncheckedBranches says once, per run, which branches this could not
// ask about -- not once per branch. `github.token` is scoped to the repository
// being built, so a cross-repo check against a private one is REFUSED as a
// matter of course, and a per-line warning would fire on every run of every
// repo forever. A guard that cries wolf that often is one nobody reads by the
// time it is right.
func reportUncheckedBranches(unchecked []string) {
	if len(unchecked) == 0 {
		return
	}
	logger.Warn("could not check whether these tracked branches have an open pull request: %s. A cross-repository check needs a GITHUB_TOKEN or GH_TOKEN that can read the other repository's pull requests; the run's own token is scoped to this repository. Until then a branch that a merge is about to delete is not caught here.",
		strings.Join(unchecked, ", "))
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
// request in the repository mod belongs to, and whether the question could be
// asked at all.
//
// An unanswerable question is never a finding, and never a failed build: a
// guard that turned an unreachable API into a red run would fail over the
// network rather than over the thing it checks. It is not silent either --
// the caller collects these and says so once.
func checkTemporaryBranch(mod, branch string) (found temporaryBranch, isTemporary, checked bool) {
	owner, repo, ok := gitHubOwnerRepo(mod)
	if !ok {
		return temporaryBranch{}, false, true // no GitHub, no pull requests to have
	}

	api := fmt.Sprintf("https://api.github.com/repos/%s/%s/pulls?state=open&per_page=1&head=%s",
		owner, repo, url.QueryEscape(owner+":"+branch))
	req, err := http.NewRequest(http.MethodGet, api, nil)
	if err != nil {
		return temporaryBranch{}, false, false
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token := gitHubAPIToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return temporaryBranch{}, false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return temporaryBranch{}, false, false
	}

	var open []struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&open); err != nil {
		return temporaryBranch{}, false, false
	}
	if len(open) == 0 {
		return temporaryBranch{}, false, true
	}
	pr := open[0].HTMLURL
	if pr == "" {
		pr = fmt.Sprintf("%s/%s#%d", owner, repo, open[0].Number)
	}
	return temporaryBranch{branch: branch, pr: pr}, true, true
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
