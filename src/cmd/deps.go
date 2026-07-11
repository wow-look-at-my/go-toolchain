package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logx"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
	"golang.org/x/mod/modfile"
)

// How long to cache "up-to-date" results before rechecking
const upToDateCacheDuration = time.Minute

// depsCache persists dependency-check results across runs. The production
// implementation is sqlite-backed (depscache_sqlite.go); GOOS=cosmo builds
// get a no-op cache instead (depscache_cosmo.go) because modernc.org/sqlite
// drags in modernc.org/libc, whose per-GOOS generated code has no cosmo
// target.
type depsCache interface {
	// lookup returns the cached entry for (path, version). found=false means
	// no entry. update != "" means the module was cached as outdated (outdated
	// entries never expire); update == "" means it was cached as up-to-date at
	// checkedAt (unix seconds).
	lookup(path, version string) (update string, checkedAt int64, found bool)
	// store records a check result performed at checkedAt (unix seconds);
	// update == "" means up-to-date.
	store(path, version, update string, checkedAt int64)
	close()
}

// OutdatedDep represents a dependency with an available update
type OutdatedDep struct {
	Path    string // module path
	Version string // current version
	Update  string // available update version
}

// DepChecker handles async dependency checking with caching
type DepChecker struct {
	cache        depsCache
	results      []OutdatedDep
	total        int
	checked      int
	done         bool
	err          error
	mu           sync.Mutex
	doneCh       chan struct{}
	canceled     bool
	listDepsTime time.Duration // time spent listing direct deps
	liveChecks   int           // number of live (non-cached) checks performed
	start        time.Time     // when the check was started (for timeline)
}

// CheckOutdatedDeps starts an async check for outdated dependencies.
// Returns a DepChecker that can be used to wait for results with progress.
func CheckOutdatedDeps() *DepChecker {
	dc := &DepChecker{
		doneCh: make(chan struct{}),
		start:  time.Now(),
	}

	go dc.run()
	return dc
}

func (dc *DepChecker) run() {
	defer close(dc.doneCh)

	// Open/create the persistent result cache
	cache, err := openDepsCache()
	if err != nil {
		dc.mu.Lock()
		dc.err = err
		dc.done = true
		dc.mu.Unlock()
		return
	}
	dc.cache = cache
	defer cache.close()

	// Get list of direct dependencies
	listStart := time.Now()
	deps, err := listDirectDeps()
	dc.mu.Lock()
	dc.listDepsTime = time.Since(listStart)
	dc.mu.Unlock()
	if err != nil {
		dc.mu.Lock()
		dc.err = err
		dc.done = true
		dc.mu.Unlock()
		return
	}

	dc.mu.Lock()
	dc.total = len(deps)
	dc.mu.Unlock()

	// Check each dependency
	var outdated []OutdatedDep
	for _, dep := range deps {
		dc.mu.Lock()
		if dc.canceled {
			dc.mu.Unlock()
			break
		}
		dc.checked++
		dc.mu.Unlock()

		// Only check pseudo-versions
		if !looksLikeGitVersion(dep.Version) {
			continue
		}

		update, needsUpdate, err := dc.checkDep(dep.Path, dep.Version)
		if err != nil {
			continue // Skip on error, don't fail the whole check
		}

		if needsUpdate {
			outdated = append(outdated, OutdatedDep{
				Path:    dep.Path,
				Version: dep.Version,
				Update:  update,
			})
		}
	}

	dc.mu.Lock()
	dc.results = outdated
	dc.done = true
	dc.mu.Unlock()
}

// checkDep checks if a dependency has an update, using cache when valid
func (dc *DepChecker) checkDep(path, version string) (update string, needsUpdate bool, err error) {
	now := time.Now().Unix()

	// Check cache first
	if cachedUpdate, checkedAt, found := dc.cache.lookup(path, version); found {
		if cachedUpdate != "" {
			// Cached as outdated - return immediately (no expiry for outdated)
			return cachedUpdate, true, nil
		}
		// Cached as up-to-date - check if still fresh
		if now-checkedAt < int64(upToDateCacheDuration.Seconds()) {
			return "", false, nil
		}
	}

	// Cache miss or expired - check live
	dc.mu.Lock()
	dc.liveChecks++
	dc.mu.Unlock()
	update, needsUpdate, err = checkDepLive(path)
	if err != nil {
		return "", false, err
	}

	// Update cache (update is "" when up-to-date)
	dc.cache.store(path, version, update, now)

	return update, needsUpdate, nil
}

// checkDepLive queries the Go module proxy for a module's latest version.
// It uses a direct HTTP request to the GOPROXY instead of shelling out to
// "go list -m -u" which can be unreliable and slow in CI environments.
func checkDepLive(path string) (update string, needsUpdate bool, err error) {
	proxy := os.Getenv("GOPROXY")
	// GOPROXY can be a comma-separated list; use the first proxy entry (skip "direct"/"off")
	var found string
	for _, entry := range strings.FieldsFunc(proxy, func(r rune) bool { return r == ',' || r == '|' }) {
		entry = strings.TrimSpace(entry)
		if entry != "" && entry != "off" && entry != "direct" {
			found = entry
			break
		}
	}
	if found == "" {
		return "", false, fmt.Errorf("no usable proxy in GOPROXY=%q", os.Getenv("GOPROXY"))
	}
	proxy = found

	// Ensure proxy URL has a scheme
	if !strings.HasPrefix(proxy, "http://") && !strings.HasPrefix(proxy, "https://") {
		proxy = "https://" + proxy
	}

	// Query $GOPROXY/<module>/@latest
	// Module paths are case-encoded per https://pkg.go.dev/golang.org/x/mod/module#EscapePath
	escapedPath, err := escapePath(path)
	if err != nil {
		return "", false, err
	}
	url := proxy + "/" + escapedPath + "/@latest"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", false, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", false, fmt.Errorf("proxy returned %d for %s", resp.StatusCode, path)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", false, err
	}

	var info struct {
		Version string `json:"Version"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return "", false, err
	}

	if info.Version != "" {
		return info.Version, true, nil
	}
	return "", false, nil
}

// escapePath converts a module path to its case-encoded form for proxy URLs.
// Upper-case letters are replaced with '!' followed by the lower-case letter.
func escapePath(path string) (string, error) {
	var b strings.Builder
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			b.WriteRune(r + ('a' - 'A'))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String(), nil
}

type depInfo struct {
	Path    string
	Version string
}

// findGoMod walks up from the current directory to find go.mod.
func findGoMod() string {
	dir, err := os.Getwd()
	if err != nil {
		return "go.mod"
	}
	for {
		p := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(p); err == nil {
			return p
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "go.mod"
		}
		dir = parent
	}
}

// listDirectDeps returns all direct (non-indirect) dependencies by parsing
// go.mod directly, avoiding the expensive `go list -m -json all` which forces
// full module graph resolution (~60s on cold cache).
func listDirectDeps() ([]depInfo, error) {
	goModPath := findGoMod()
	data, err := os.ReadFile(goModPath)
	if err != nil {
		return nil, err
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}

	var deps []depInfo
	for _, req := range f.Require {
		if req.Indirect {
			continue
		}
		deps = append(deps, depInfo{Path: req.Mod.Path, Version: req.Mod.Version})
	}
	return deps, nil
}

// looksLikeGitVersion returns true if the version appears to be a pseudo-version
func looksLikeGitVersion(version string) bool {
	parts := strings.Split(version, "-")
	if len(parts) < 3 {
		return false
	}

	// Check if last part looks like a commit hash (12 hex chars)
	lastPart := parts[len(parts)-1]
	return len(lastPart) == 12 && isHex(lastPart)
}

func isHex(s string) bool {
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// Progress returns current check progress (checked, total)
func (dc *DepChecker) Progress() (checked, total int) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.checked, dc.total
}

// Done returns true if the check has completed
func (dc *DepChecker) Done() bool {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	return dc.done
}

// Cancel stops the check early
func (dc *DepChecker) Cancel() {
	dc.mu.Lock()
	dc.canceled = true
	dc.mu.Unlock()
}

// WaitWithProgress waits for the check to complete, showing progress if it takes too long.
// Returns the list of outdated dependencies.
func (dc *DepChecker) WaitWithProgress() []OutdatedDep {
	if dc == nil {
		return nil
	}

	// Set up Ctrl+C handler to skip
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			dc.Cancel()
			cancel()
		case <-ctx.Done():
		}
	}()

	// Wait with progress display (throttled to 1/sec, no reprints if unchanged)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	showProgress := false
	lastPct := -1
	startWait := time.Now()

	for {
		select {
		case <-dc.doneCh:
			elapsed := time.Since(startWait)
			if showProgress {
				fmt.Printf(" %sdone.%s %s\n", colorGreen, colorReset, logx.FmtDuration(elapsed))
			}
			dc.mu.Lock()
			result := dc.results
			listTime := dc.listDepsTime
			liveChecks := dc.liveChecks
			checked := dc.checked
			total := dc.total
			dc.mu.Unlock()
			if showProgress && elapsed > 5*time.Second {
				fmt.Printf("    deps: list=%s, checked=%d/%d (%d live)\n",
					logx.FmtDuration(listTime), checked, total, liveChecks)
			}
			return result
		case <-ticker.C:
			// Show progress if waiting more than 500ms
			if time.Since(startWait) > 500*time.Millisecond {
				checked, total := dc.Progress()
				pct := 0
				if total > 0 {
					pct = checked * 100 / total
				}
				// Only print if progress changed
				if pct != lastPct {
					if !showProgress {
						showProgress = true
						fmt.Printf("Checking for dependency updates... %d%%", pct)
					} else {
						fmt.Printf(" %d%%", pct)
					}
					lastPct = pct
				}
			}
		case <-ctx.Done():
			if showProgress {
				fmt.Println(" skipped")
			}
			return nil
		}
	}
}

// PrintOutdatedDeps prints warnings for outdated dependencies
func PrintOutdatedDeps(deps []OutdatedDep) {
	if len(deps) == 0 {
		return
	}

	fmt.Println()
	fmt.Println(warn("Outdated git dependencies:"))
	for _, dep := range deps {
		current := shortenVersion(dep.Version)
		update := shortenVersion(dep.Update)
		fmt.Printf("    %s: %s -> %s\n", dep.Path, current, update)
	}
	fmt.Println("    Run 'go get -u' to update")
}

// shortenVersion shortens a pseudo-version for display
func shortenVersion(v string) string {
	parts := strings.Split(v, "-")
	if len(parts) >= 3 {
		hash := parts[len(parts)-1]
		if len(hash) >= 7 {
			return hash[:7]
		}
		return hash
	}
	return v
}

// WaitForOutdatedDeps waits for the dependency check to complete and prints results.
// Dependencies from the same org/prefix as the current module are automatically updated.
// Returns true if any dependencies were auto-updated (caller should rebuild).
func WaitForOutdatedDeps(dc *DepChecker) bool {
	if dc == nil {
		return false
	}
	deps := dc.WaitWithProgress()

	// Record to the pipeline timeline
	if pipelineTimeline != nil {
		pipelineTimeline.Record("Dependency check", "deps", dc.start, time.Now(), dc.err != nil)
	}

	// Get auto-update prefix from current module
	autoUpdatePrefix := getAutoUpdatePrefix()

	// Separate auto-update deps from manual deps
	var toAutoUpdate, manual []OutdatedDep
	for _, dep := range deps {
		if autoUpdatePrefix != "" && strings.HasPrefix(dep.Path, autoUpdatePrefix) {
			toAutoUpdate = append(toAutoUpdate, dep)
		} else {
			manual = append(manual, dep)
		}
	}

	// Auto-update trusted dependencies
	if len(toAutoUpdate) > 0 {
		autoUpdateDeps(toAutoUpdate)
	}

	// Print remaining manual deps
	PrintOutdatedDeps(manual)

	return len(toAutoUpdate) > 0
}

// getAutoUpdatePrefix returns the org prefix from the current module path.
// e.g., "github.com/org/repo" -> "github.com/org/"
// e.g., "gitlab.com/group/repo" -> "gitlab.com/group/"
func getAutoUpdatePrefix() string {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return ""
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil || f.Module == nil {
		return ""
	}

	// Extract host + org: "host.com/org/repo" -> "host.com/org/"
	parts := strings.Split(f.Module.Mod.Path, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1] + "/"
	}
	return ""
}

// autoUpdateDeps runs go get -u for each dependency
func autoUpdateDeps(deps []OutdatedDep) {
	fmt.Println()
	s := logStep("Auto-updating trusted dependencies")
	s.noteOutput()
	for _, dep := range deps {
		current := shortenVersion(dep.Version)
		update := shortenVersion(dep.Update)
		fmt.Printf("    %s: %s -> %s\n", dep.Path, current, update)

		cmd := exec.Command("go", "get", "-u", dep.Path+"@latest")
		if err := cmd.Run(); err != nil {
			fmt.Printf("    %s failed to update: %v\n", warn("WARNING:"), err)
		}
	}
	// Run go mod tidy to clean up
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Run()
	s.done()
}

// FixBogusDepsVersions detects dependencies with v0.0.0 versions in go.mod and
// resolves them to actual pseudo-versions. This happens when someone adds a
// git-based dependency without a proper version tag.
func FixBogusDepsVersions(r runner.CommandRunner) error {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return nil // Let go mod tidy handle missing go.mod
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil // Let go mod tidy handle parse errors
	}

	var toFix []string
	for _, req := range f.Require {
		if req.Mod.Version == "v0.0.0" {
			toFix = append(toFix, req.Mod.Path)
		}
	}

	if len(toFix) == 0 {
		return nil
	}

	// Resolve each module to its actual latest version
	for _, mod := range toFix {
		if !jsonOutput {
			fmt.Printf("⇒ Resolving %s (v0.0.0 is not a valid version)\n", mod)
		}

		version, err := resolveLatestVersionViaGit(r, mod)
		if err != nil {
			return fmt.Errorf("failed to resolve %s: %w", mod, err)
		}

		// Update the require in the parsed file
		if err := f.AddRequire(mod, version); err != nil {
			return fmt.Errorf("failed to update %s: %w", mod, err)
		}
	}

	// Write the updated go.mod
	newData, err := f.Format()
	if err != nil {
		return fmt.Errorf("failed to format go.mod: %w", err)
	}
	if err := os.WriteFile("go.mod", newData, 0644); err != nil {
		return fmt.Errorf("failed to write go.mod: %w", err)
	}

	return nil
}

// resolveLatestVersionViaGit fetches the latest commit from a git repo and
// constructs a proper pseudo-version with the correct timestamp.
func resolveLatestVersionViaGit(r runner.CommandRunner, mod string) (string, error) {
	gitURL := "https://" + mod

	// Get HEAD commit hash via ls-remote
	proc, err := runner.Cmd("git", "ls-remote", gitURL, "HEAD").WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git ls-remote failed: %w", err)
	}
	output, _ := io.ReadAll(proc.Stdout())
	if proc.Wait() != nil {
		return "", fmt.Errorf("git ls-remote failed: %w", err)
	}

	fields := strings.Fields(string(output))
	if len(fields) < 1 {
		return "", fmt.Errorf("no HEAD ref found")
	}
	fullHash := fields[0]
	if len(fullHash) < 12 {
		return "", fmt.Errorf("invalid commit hash: %s", fullHash)
	}
	shortHash := fullHash[:12]

	// Shallow fetch just the commit to get its timestamp
	tmpDir, err := os.MkdirTemp("", "resolve-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmpDir)

	// Init bare repo and fetch just the one commit
	proc, err = runner.Cmd("git", "-C", tmpDir, "init", "--bare").WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git init failed: %w", err)
	}
	if proc.Wait() != nil {
		return "", fmt.Errorf("git init failed: %w", err)
	}

	proc, err = runner.Cmd("git", "-C", tmpDir, "fetch", "--depth=1", gitURL, fullHash).WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git fetch failed: %w", err)
	}
	if proc.Wait() != nil {
		return "", fmt.Errorf("git fetch failed: %w", err)
	}

	// Get commit timestamp in UTC (use Unix epoch and convert)
	proc, err = runner.Cmd("git", "-C", tmpDir, "log", "-1", "--format=%ct", fullHash).WithQuiet().Run(r)
	if err != nil {
		return "", fmt.Errorf("git log failed: %w", err)
	}
	tsOutput, _ := io.ReadAll(proc.Stdout())
	if proc.Wait() != nil {
		return "", fmt.Errorf("git log failed: %w", err)
	}

	epochStr := strings.TrimSpace(string(tsOutput))
	epoch, err := strconv.ParseInt(epochStr, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid timestamp: %s", epochStr)
	}
	timestamp := time.Unix(epoch, 0).UTC().Format("20060102150405")

	return fmt.Sprintf("v0.0.0-%s-%s", timestamp, shortHash), nil
}
