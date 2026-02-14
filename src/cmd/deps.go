package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// How long to cache "up-to-date" results before rechecking
	upToDateCacheDuration = time.Minute

	// Cache is stored in ~/.cache/go-toolchain/deps.db
	cacheSubdir = "go-toolchain"
	cacheFile   = "deps.db"
)

// OutdatedDep represents a dependency with an available update
type OutdatedDep struct {
	Path    string // module path
	Version string // current version
	Update  string // available update version
}

// DepChecker handles async dependency checking with caching
type DepChecker struct {
	db       *sql.DB
	results  []OutdatedDep
	total    int
	checked  int
	done     bool
	err      error
	mu       sync.Mutex
	doneCh   chan struct{}
	canceled bool
}

// CheckOutdatedDeps starts an async check for outdated dependencies.
// Returns a DepChecker that can be used to wait for results with progress.
func CheckOutdatedDeps() *DepChecker {
	dc := &DepChecker{
		doneCh: make(chan struct{}),
	}

	go dc.run()
	return dc
}

func (dc *DepChecker) run() {
	defer close(dc.doneCh)

	// Open/create cache database
	db, err := openCacheDB()
	if err != nil {
		dc.mu.Lock()
		dc.err = err
		dc.done = true
		dc.mu.Unlock()
		return
	}
	dc.db = db
	defer db.Close()

	// Get list of direct dependencies
	deps, err := listDirectDeps()
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
	var cachedUpdate sql.NullString
	var checkedAt int64
	err = dc.db.QueryRow(
		`SELECT update_version, checked_at FROM deps WHERE path = ? AND version = ?`,
		path, version,
	).Scan(&cachedUpdate, &checkedAt)

	if err == nil {
		// Cache hit
		if cachedUpdate.Valid {
			// Cached as outdated - return immediately (no expiry for outdated)
			return cachedUpdate.String, true, nil
		}
		// Cached as up-to-date - check if still fresh
		if now-checkedAt < int64(upToDateCacheDuration.Seconds()) {
			return "", false, nil
		}
	}

	// Cache miss or expired - check live
	update, needsUpdate, err = checkDepLive(path)
	if err != nil {
		return "", false, err
	}

	// Update cache
	var updateVal sql.NullString
	if needsUpdate {
		updateVal = sql.NullString{String: update, Valid: true}
	}
	_, _ = dc.db.Exec(
		`INSERT OR REPLACE INTO deps (path, version, update_version, checked_at) VALUES (?, ?, ?, ?)`,
		path, version, updateVal, now,
	)

	return update, needsUpdate, nil
}

// checkDepLive queries go list for a single module's update status
func checkDepLive(path string) (update string, needsUpdate bool, err error) {
	cmd := exec.Command("go", "list", "-m", "-u", "-json", path)
	output, err := cmd.Output()
	if err != nil {
		return "", false, err
	}

	var mod struct {
		Update *struct {
			Version string
		}
	}
	if err := json.Unmarshal(output, &mod); err != nil {
		return "", false, err
	}

	if mod.Update != nil {
		return mod.Update.Version, true, nil
	}
	return "", false, nil
}

type depInfo struct {
	Path    string
	Version string
}

// listDirectDeps returns all direct (non-indirect) dependencies
func listDirectDeps() ([]depInfo, error) {
	cmd := exec.Command("go", "list", "-m", "-json", "all")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var deps []depInfo
	decoder := json.NewDecoder(stdout)

	for decoder.More() {
		var mod struct {
			Path     string
			Version  string
			Main     bool
			Indirect bool
		}
		if err := decoder.Decode(&mod); err != nil {
			continue
		}

		if mod.Main || mod.Indirect {
			continue
		}

		deps = append(deps, depInfo{Path: mod.Path, Version: mod.Version})
	}

	cmd.Wait()
	return deps, nil
}

func openCacheDB() (*sql.DB, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		cacheDir = filepath.Join(os.Getenv("HOME"), ".cache")
	}

	dir := filepath.Join(cacheDir, cacheSubdir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dir, cacheFile)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// Create table if not exists
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS deps (
			path TEXT NOT NULL,
			version TEXT NOT NULL,
			update_version TEXT,
			checked_at INTEGER NOT NULL,
			PRIMARY KEY (path, version)
		)
	`)
	if err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
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

	// Wait with progress display
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	showProgress := false
	startWait := time.Now()

	for {
		select {
		case <-dc.doneCh:
			if showProgress {
				// Clear progress line
				fmt.Print("\r\033[K")
			}
			dc.mu.Lock()
			result := dc.results
			dc.mu.Unlock()
			return result
		case <-ticker.C:
			// Show progress if waiting more than 500ms
			if time.Since(startWait) > 500*time.Millisecond {
				showProgress = true
				checked, total := dc.Progress()
				if total > 0 {
					pct := checked * 100 / total
					fmt.Printf("\rChecking for dependency updates... %d%% (Ctrl+C to skip)", pct)
				} else {
					fmt.Print("\rChecking for dependency updates... (Ctrl+C to skip)")
				}
			}
		case <-ctx.Done():
			if showProgress {
				fmt.Print("\r\033[K")
				fmt.Println("Dependency check skipped")
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

// WaitForOutdatedDeps waits for the dependency check to complete and prints results
func WaitForOutdatedDeps(dc *DepChecker) {
	if dc == nil {
		return
	}
	deps := dc.WaitWithProgress()
	PrintOutdatedDeps(deps)
}
