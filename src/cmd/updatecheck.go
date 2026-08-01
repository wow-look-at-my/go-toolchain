package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// go-toolchain binaries are published to buildhost (pazer.build). The background
// update check asks buildhost for the newest published go-toolchain release and
// compares it against this binary's own VCS-stamped commit, warning when a newer
// build is available. The base URL can be overridden with
// GO_TOOLCHAIN_BUILDHOST_URL (for a self-hosted buildhost); these stay vars so
// tests can point them at a local server.
var (
	buildhostAPIBase = envOr("GO_TOOLCHAIN_BUILDHOST_URL", "https://pazer.build")
	buildhostProject = "go-toolchain"
)

// updateCheck is a single in-flight background update check. The network work
// runs in a goroutine; ReportUpdateCheck prints the result if the goroutine
// finished, or cancels it if it did not. It never blocks the main flow.
type updateCheck struct {
	cancel context.CancelFunc
	done   chan struct{}
	msg    string // written by the goroutine, read only after <-done
	once   sync.Once
}

// activeUpdateCheck is the process-wide background check started by
// StartUpdateCheck, or nil when no check is running (disabled / not started).
var activeUpdateCheck *updateCheck

// StartUpdateCheck kicks off a non-blocking background check for a newer
// go-toolchain on buildhost. It returns immediately; the result is surfaced
// later by ReportUpdateCheck. The work runs in a goroutine with a cancelable
// context so it can be killed the moment the main work is done. The check
// always runs (it cannot be disabled); it is silent on any error, so it never
// gets in the way.
func StartUpdateCheck() {
	ctx, cancel := context.WithCancel(context.Background())
	uc := &updateCheck{cancel: cancel, done: make(chan struct{})}
	activeUpdateCheck = uc
	go func() {
		defer close(uc.done)
		uc.msg = computeUpdateWarning(ctx)
	}()
}

// ReportUpdateCheck surfaces the background check started by StartUpdateCheck.
// If the check already finished, it prints a warning to stderr when a newer
// release exists. If the check is still in flight, it cancels (kills) the
// request and returns immediately — the update check must never delay or block
// go-toolchain. Safe to call when no check was started, and idempotent, so it
// can be invoked on every exit path.
func ReportUpdateCheck() {
	uc := activeUpdateCheck
	if uc == nil {
		return
	}
	uc.once.Do(func() {
		select {
		case <-uc.done:
			// Finished in time: surface the result (msg is "" when up to date).
			if uc.msg != "" {
				logger.Warn("%s", uc.msg)
			}
		default:
			// Not finished by the time the main work is done: kill it and move
			// on without waiting on the goroutine.
			uc.cancel()
		}
	})
}

// computeUpdateWarning fetches the latest published go-toolchain release from
// buildhost and returns a warning string if this binary is out of date, or ""
// if it is current, cannot be compared, or the check failed. It is silent on
// every error — an update check must never get in the way.
func computeUpdateWarning(ctx context.Context) string {
	myCommit := resolvedCommit()
	myTs, ok := resolvedTimestamp()
	if myCommit == "unknown" || !ok {
		return "" // dev build with no VCS stamp — nothing to compare against
	}

	latest, err := fetchLatestBuildhostRelease(ctx)
	if err != nil {
		return ""
	}

	// Same commit as the newest published release: we are already up to date.
	if commitsMatch(latest.GitCommit, myCommit) {
		return ""
	}

	// Different commit. Only warn when the latest release is genuinely newer than
	// this build, so a binary built from an unpublished/ahead commit stays quiet.
	latestTime := latest.timestamp()
	if latestTime.IsZero() || !latestTime.After(time.Unix(myTs, 0)) {
		return ""
	}

	return fmt.Sprintf("%s⇒ go-toolchain is out of date: %s.%s\n"+
		"  Update from buildhost: https://dl.pazer.build/go-toolchain "+
		"(or `brew upgrade`, `npm update`, `apt upgrade`).",
		colorYellow, describeStaleness(ctx, myCommit, time.Unix(myTs, 0), latest, latestTime), colorReset,
	)
}

// describeStaleness renders the half of the warning a reader actually decides
// on: how far behind this binary is, with both sides expressed the same way.
//
// The old wording gave the latest release's version and age, but described THIS
// binary as a git hash and a calendar date -- so neither axis could be compared
// without stopping to do arithmetic, and the hash answered a question nobody
// asked. What matters is: which version am I, which is current, and how far
// apart are they.
//
// This binary does not know its own version number (buildhost assigns it at
// publish time, after the build), so it is looked up by commit among the recent
// releases. That lookup is best-effort: when it fails, or when this build was
// never published at all, the message falls back to comparing ages -- and only
// then names the commit, because then it is the only identity there is.
func describeStaleness(ctx context.Context, myCommit string, myTime time.Time, latest *buildhostRelease, latestTime time.Time) string {
	latestPart := fmt.Sprintf("latest is v%s (%s old)", latest.Version, formatDuration(time.Since(latestTime)))
	mineAge := formatDuration(time.Since(myTime))

	if mine, behind, ok := findOwnRelease(ctx, myCommit); ok {
		return fmt.Sprintf("you have v%s (%s old), %s -- %s behind",
			mine.Version, mineAge, latestPart, plural(behind, "release"))
	}

	short := myCommit
	if len(short) > 7 {
		short = short[:7]
	}
	return fmt.Sprintf("your build is %s old (commit %s, not a published release), %s",
		mineAge, short, latestPart)
}

// plural renders a count with its noun, pluralized.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// findOwnRelease locates this binary's own release among buildhost's recent
// ones and reports how many published releases are newer than it. The listing
// is newest-first, so that count is simply the index. Bounded on purpose: a
// binary older than the window is old enough that the exact number adds
// nothing the age has not already said.
func findOwnRelease(ctx context.Context, myCommit string) (rel *buildhostRelease, behind int, ok bool) {
	releases, err := fetchBuildhostReleases(ctx, ownReleaseLookupLimit)
	if err != nil {
		return nil, 0, false
	}
	for i := range releases {
		if commitsMatch(releases[i].GitCommit, myCommit) {
			return &releases[i], i, true
		}
	}
	return nil, 0, false
}

// ownReleaseLookupLimit bounds the release listing fetched to identify this
// binary. It is a background request that is cancelled the moment the build
// finishes, so the cost is usually zero -- but it should not be unbounded.
const ownReleaseLookupLimit = 200

// fetchBuildhostReleases lists a project's releases, newest first.
func fetchBuildhostReleases(ctx context.Context, limit int) ([]buildhostRelease, error) {
	url := fmt.Sprintf("%s/api/v1/projects/%s/releases?limit=%d", buildhostAPIBase, buildhostProject, limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("buildhost returned HTTP %d", resp.StatusCode)
	}
	var releases []buildhostRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// buildhostRelease is the subset of buildhost's release JSON the check needs.
type buildhostRelease struct {
	Version     string     `json:"version"`
	VersionNum  int64      `json:"version_num"`
	GitCommit   string     `json:"git_commit"`
	GitBranch   string     `json:"git_branch"`
	Published   bool       `json:"published"`
	CreatedAt   time.Time  `json:"created_at"`
	PublishedAt *time.Time `json:"published_at"`
}

// timestamp returns the most meaningful release time: when it was published,
// falling back to when it was created.
func (r *buildhostRelease) timestamp() time.Time {
	if r.PublishedAt != nil && !r.PublishedAt.IsZero() {
		return *r.PublishedAt
	}
	return r.CreatedAt
}

// fetchLatestBuildhostRelease asks buildhost for the newest published
// go-toolchain release via the public REST API. The request honors ctx, so a
// cancel from ReportUpdateCheck aborts an in-flight call promptly.
func fetchLatestBuildhostRelease(ctx context.Context) (*buildhostRelease, error) {
	url := fmt.Sprintf("%s/api/v1/projects/%s/releases/latest", buildhostAPIBase, buildhostProject)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("buildhost returned HTTP %d", resp.StatusCode)
	}
	var rel buildhostRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// commitsMatch reports whether two git commit identifiers refer to the same
// commit, tolerating short/long SHA forms (one a prefix of the other) and case.
func commitsMatch(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a = strings.ToLower(a)
	b = strings.ToLower(b)
	if len(a) > len(b) {
		a, b = b, a
	}
	return strings.HasPrefix(b, a)
}
