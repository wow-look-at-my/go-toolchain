package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
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

// updateCheckEnvVar disables the background update check when set to a truthy
// value (e.g. air-gapped environments or tests). The check is on by default.
const updateCheckEnvVar = "GO_TOOLCHAIN_NO_UPDATE_CHECK"

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
// context so it can be killed the moment the main work is done. Set
// GO_TOOLCHAIN_NO_UPDATE_CHECK to a truthy value to disable it entirely.
func StartUpdateCheck() {
	if envTruthy(os.Getenv(updateCheckEnvVar)) {
		return
	}
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
				fmt.Fprintln(os.Stderr, uc.msg)
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

	short := myCommit
	if len(short) > 7 {
		short = short[:7]
	}
	return fmt.Sprintf(
		"%s⇒ go-toolchain is out of date: latest is v%s (published %s ago); "+
			"you are running %s from %s.%s\n"+
			"  Update from buildhost: https://dl.pazer.build/go-toolchain "+
			"(or `brew upgrade`, `npm update`, `apt upgrade`).",
		colorYellow, latest.Version, formatDuration(time.Since(latestTime)),
		short, time.Unix(myTs, 0).UTC().Format("2006-01-02"), colorReset,
	)
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
