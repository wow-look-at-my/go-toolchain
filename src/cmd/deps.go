package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/mod/modfile"

	"github.com/wow-look-at-my/go-containers/set"
)

// How long to cache "up-to-date" results before rechecking
const upToDateCacheDuration = time.Minute

// depsCache persists dependency-check results across runs, in a JSON file
// (depscache_file.go). It is on in every binary: the store is small enough to
// need no engine, and a build tag here would take the cache away from
// whatever the tag excludes. Depth: docs/DEPS.md
type depsCache interface {
	// lookup returns the cached entry: update != "" means cached outdated (never expires); found=false means no entry.
	lookup(path, version string) (update string, checkedAt int64, found bool)
	// store records a check result at checkedAt (unix seconds); update == "" means up-to-date.
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

// CheckOutdatedDeps starts an async check and returns a DepChecker to poll for progress.
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

		// Tracked deps are owned by UpdateTrackedBranchDeps; checking @latest here
		// would drag such a line back onto the default branch.
		if dep.Tracked {
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

	// Check the cache before asking the proxy
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
	// GOPROXY can be a comma-separated list; use the leading proxy entry (skip "direct"/"off")
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

	// Query $GOPROXY/<module>/@latest; module paths are case-encoded per module#EscapePath.
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
	Tracked bool // the line, or the replace covering it, carries a tracking marker
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

	// A require replaced by a tracked replacement is tracked too: the build uses the replacement's version.
	replacedTracked := set.New[string]()
	for _, rep := range f.Replace {
		if isTracked(rep.Syntax) {
			replacedTracked.Add(rep.Old.Path)
		}
	}

	var deps []depInfo
	for _, req := range f.Require {
		if req.Indirect {
			continue
		}
		deps = append(deps, depInfo{
			Path:    req.Mod.Path,
			Version: req.Mod.Version,
			Tracked: isTracked(req.Syntax) || replacedTracked.Contains(req.Mod.Path),
		})
	}
	return deps, nil
}

// looksLikeGitVersion returns true if the version appears to be a pseudo-version
func looksLikeGitVersion(version string) bool {
	parts := strings.Split(version, "-")
	if len(parts) < 3 {
		return false
	}

	// Check whether the trailing part looks like a short commit hash
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
