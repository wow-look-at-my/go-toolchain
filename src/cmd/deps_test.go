package cmd

import "testing"

func TestLooksLikeGitVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		// Pseudo-versions (should match)
		{"v0.0.0-20240101120000-abc123def456", true},
		{"v1.2.3-0.20240101120000-abc123def456", true},
		{"v0.0.0-20230915123456-1234567890ab", true},

		// Tagged versions (should not match)
		{"v1.0.0", false},
		{"v1.2.3", false},
		{"v2.0.0-beta.1", false},
		{"v1.0.0-rc1", false},

		// Edge cases
		{"", false},
		{"v1", false},
		{"v1.0", false},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := looksLikeGitVersion(tt.version)
			if got != tt.want {
				t.Errorf("looksLikeGitVersion(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

func TestIsHex(t *testing.T) {
	tests := []struct {
		s    string
		want bool
	}{
		{"abc123def456", true},
		{"0123456789ab", true},
		{"000000000000", true},
		{"ffffffffffff", true},
		{"ABC123", false}, // uppercase not allowed
		{"ghijkl", false}, // non-hex letters
		{"12345", true},   // shorter is fine
		{"", true},        // empty string has no non-hex chars
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			got := isHex(tt.s)
			if got != tt.want {
				t.Errorf("isHex(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestShortenVersion(t *testing.T) {
	tests := []struct {
		version string
		want    string
	}{
		// Pseudo-versions get shortened to first 7 chars of hash
		{"v0.0.0-20240101120000-abc123def456", "abc123d"},
		{"v1.2.3-0.20240101120000-1234567890ab", "1234567"},

		// Regular versions pass through unchanged
		{"v1.0.0", "v1.0.0"},
		{"v2.3.4", "v2.3.4"},

		// Short hash (less than 7 chars) stays as-is
		{"v0.0.0-20240101-abc", "abc"},
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := shortenVersion(tt.version)
			if got != tt.want {
				t.Errorf("shortenVersion(%q) = %q, want %q", tt.version, got, tt.want)
			}
		})
	}
}

func TestPrintOutdatedDeps_Empty(t *testing.T) {
	// Should not panic with empty slice
	PrintOutdatedDeps(nil)
	PrintOutdatedDeps([]OutdatedDep{})
}

func TestPrintOutdatedDeps_WithDeps(t *testing.T) {
	deps := []OutdatedDep{
		{
			Path:    "example.com/foo",
			Version: "v0.0.0-20240101120000-abc123def456",
			Update:  "v0.0.0-20240201120000-def456abc123",
		},
	}
	// Should not panic
	PrintOutdatedDeps(deps)
}

func TestWaitForOutdatedDeps_Nil(t *testing.T) {
	// Should not panic with nil DepChecker
	WaitForOutdatedDeps(nil)
}

func TestDepChecker_Progress(t *testing.T) {
	dc := &DepChecker{
		checked: 5,
		total:   10,
	}
	checked, total := dc.Progress()
	if checked != 5 || total != 10 {
		t.Errorf("Progress() = %d, %d; want 5, 10", checked, total)
	}
}

func TestDepChecker_Done(t *testing.T) {
	dc := &DepChecker{done: false}
	if dc.Done() {
		t.Error("Done() = true; want false")
	}
	dc.done = true
	if !dc.Done() {
		t.Error("Done() = false; want true")
	}
}

func TestDepChecker_Cancel(t *testing.T) {
	dc := &DepChecker{}
	if dc.canceled {
		t.Error("canceled should be false initially")
	}
	dc.Cancel()
	if !dc.canceled {
		t.Error("canceled should be true after Cancel()")
	}
}

func TestCheckOutdatedDeps(t *testing.T) {
	// This test verifies the function doesn't panic and returns a DepChecker
	dc := CheckOutdatedDeps()
	if dc == nil {
		t.Error("CheckOutdatedDeps() returned nil")
	}
	// Wait for completion
	<-dc.doneCh
	// Should be done now
	if !dc.Done() {
		t.Error("Done() should be true after doneCh closes")
	}
}

func TestOpenCacheDB(t *testing.T) {
	db, err := openCacheDB()
	if err != nil {
		t.Fatalf("openCacheDB() error: %v", err)
	}
	defer db.Close()

	// Verify table exists by inserting and querying
	_, err = db.Exec(`INSERT OR REPLACE INTO deps (path, version, update_version, checked_at) VALUES (?, ?, ?, ?)`,
		"test/module", "v1.0.0", nil, 12345)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	var path string
	err = db.QueryRow(`SELECT path FROM deps WHERE path = ?`, "test/module").Scan(&path)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if path != "test/module" {
		t.Errorf("path = %q; want %q", path, "test/module")
	}
}

func TestListDirectDeps(t *testing.T) {
	// This runs in a real Go module, so it should return deps
	deps, err := listDirectDeps()
	if err != nil {
		t.Fatalf("listDirectDeps() error: %v", err)
	}
	// We should have at least some deps (cobra, testify, etc.)
	if len(deps) == 0 {
		t.Error("listDirectDeps() returned no deps")
	}

	// Check that we got expected deps
	found := false
	for _, d := range deps {
		if d.Path == "github.com/spf13/cobra" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find github.com/spf13/cobra in deps")
	}
}

func TestDepChecker_WaitWithProgress_AlreadyDone(t *testing.T) {
	// Create a DepChecker that's already done
	dc := &DepChecker{
		doneCh: make(chan struct{}),
		done:   true,
		results: []OutdatedDep{
			{Path: "test/pkg", Version: "v0.0.0-20240101-abc123def456", Update: "v1.0.0"},
		},
	}
	close(dc.doneCh)

	result := dc.WaitWithProgress()
	if len(result) != 1 {
		t.Errorf("WaitWithProgress() returned %d results; want 1", len(result))
	}
}

func TestCheckDepLive_RealModule(t *testing.T) {
	// Test with a real module that exists
	// github.com/spf13/cobra should work
	update, needsUpdate, err := checkDepLive("github.com/spf13/cobra")
	if err != nil {
		t.Fatalf("checkDepLive() error: %v", err)
	}
	// We don't care about the result, just that it didn't error
	_ = update
	_ = needsUpdate
}

func TestDepChecker_checkDep_CacheHit(t *testing.T) {
	db, err := openCacheDB()
	if err != nil {
		t.Fatalf("openCacheDB() error: %v", err)
	}
	defer db.Close()

	dc := &DepChecker{db: db}

	// Insert a cached "outdated" entry
	_, err = db.Exec(
		`INSERT OR REPLACE INTO deps (path, version, update_version, checked_at) VALUES (?, ?, ?, ?)`,
		"test/cached-outdated", "v0.0.0-20240101-abc123def456", "v1.0.0", 9999999999,
	)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Should return cached result
	update, needsUpdate, err := dc.checkDep("test/cached-outdated", "v0.0.0-20240101-abc123def456")
	if err != nil {
		t.Fatalf("checkDep() error: %v", err)
	}
	if !needsUpdate {
		t.Error("checkDep() needsUpdate = false; want true (cached)")
	}
	if update != "v1.0.0" {
		t.Errorf("checkDep() update = %q; want %q", update, "v1.0.0")
	}
}

func TestDepChecker_checkDep_CacheFresh(t *testing.T) {
	db, err := openCacheDB()
	if err != nil {
		t.Fatalf("openCacheDB() error: %v", err)
	}
	defer db.Close()

	dc := &DepChecker{db: db}

	// Insert a fresh "up-to-date" entry (checked just now)
	now := int64(9999999999) // Far future timestamp
	_, err = db.Exec(
		`INSERT OR REPLACE INTO deps (path, version, update_version, checked_at) VALUES (?, ?, ?, ?)`,
		"test/cached-fresh", "v0.0.0-20240101-abc123def456", nil, now,
	)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Should return cached "up-to-date" result
	update, needsUpdate, err := dc.checkDep("test/cached-fresh", "v0.0.0-20240101-abc123def456")
	if err != nil {
		t.Fatalf("checkDep() error: %v", err)
	}
	if needsUpdate {
		t.Error("checkDep() needsUpdate = true; want false (cached fresh)")
	}
	if update != "" {
		t.Errorf("checkDep() update = %q; want empty", update)
	}
}

func TestDepChecker_checkDep_CacheExpired(t *testing.T) {
	db, err := openCacheDB()
	if err != nil {
		t.Fatalf("openCacheDB() error: %v", err)
	}
	defer db.Close()

	dc := &DepChecker{db: db}

	// Insert an expired "up-to-date" entry (checked long ago)
	_, err = db.Exec(
		`INSERT OR REPLACE INTO deps (path, version, update_version, checked_at) VALUES (?, ?, ?, ?)`,
		"github.com/spf13/cobra", "v1.10.2", nil, 0, // timestamp 0 = expired
	)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Should do a live check since cache is expired
	// This will update the cache
	_, _, err = dc.checkDep("github.com/spf13/cobra", "v1.10.2")
	if err != nil {
		t.Fatalf("checkDep() error: %v", err)
	}

	// Verify cache was updated
	var checkedAt int64
	err = db.QueryRow(`SELECT checked_at FROM deps WHERE path = ? AND version = ?`,
		"github.com/spf13/cobra", "v1.10.2").Scan(&checkedAt)
	if err != nil {
		t.Fatalf("SELECT failed: %v", err)
	}
	if checkedAt == 0 {
		t.Error("checked_at was not updated")
	}
}

func TestDepChecker_run_Canceled(t *testing.T) {
	dc := &DepChecker{
		doneCh:   make(chan struct{}),
		canceled: true, // pre-cancel
	}

	// Run should exit early due to cancellation
	dc.run()

	if !dc.done {
		t.Error("done should be true after run()")
	}
}
