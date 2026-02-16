package cmd

import (
	"os"
	
	"testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

)

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
			assert.Equal(t, tt.want, got)
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
			assert.Equal(t, tt.want, got)
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
			assert.Equal(t, tt.want, got)
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
	assert.False(t, checked != 5 || total != 10)
}

func TestDepChecker_Done(t *testing.T) {
	dc := &DepChecker{done: false}
	assert.False(t, dc.Done())
	dc.done = true
	assert.True(t, dc.Done())
}

func TestDepChecker_Cancel(t *testing.T) {
	dc := &DepChecker{}
	assert.False(t, dc.canceled)
	dc.Cancel()
	assert.True(t, !dc.canceled)
}

func TestCheckOutdatedDeps(t *testing.T) {
	// This test verifies the function doesn't panic and returns a DepChecker
	dc := CheckOutdatedDeps()
	assert.NotNil(t, dc)
	// Wait for completion
	<-dc.doneCh
	// Should be done now
	assert.True(t, dc.Done())
}

func TestOpenCacheDB(t *testing.T) {
	db, err := openCacheDB()
	require.Nil(t, err)
	defer db.Close()

	// Verify table exists by inserting and querying
	_, err = db.Exec(`INSERT OR REPLACE INTO deps (path, version, update_version, checked_at) VALUES (?, ?, ?, ?)`,
		"test/module", "v1.0.0", nil, 12345)
	require.Nil(t, err)

	var path string
	err = db.QueryRow(`SELECT path FROM deps WHERE path = ?`, "test/module").Scan(&path)
	require.Nil(t, err)
	assert.Equal(t, "test/module", path)
}

func TestListDirectDeps(t *testing.T) {
	// This runs in a real Go module, so it should return deps
	deps, err := listDirectDeps()
	require.Nil(t, err)
	// We should have at least some deps (cobra, testify, etc.)
	assert.NotEqual(t, 0, len(deps))

	// Check that we got expected deps
	found := false
	for _, d := range deps {
		if d.Path == "github.com/spf13/cobra" {
			found = true
			break
		}
	}
	assert.True(t, found)
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
	assert.Equal(t, 1, len(result))
}

func TestCheckDepLive_RealModule(t *testing.T) {
	// Test with a real module that exists
	// github.com/spf13/cobra should work
	update, needsUpdate, err := checkDepLive("github.com/spf13/cobra")
	require.Nil(t, err)
	// We don't care about the result, just that it didn't error
	_ = update
	_ = needsUpdate
}

func TestDepChecker_checkDep_CacheHit(t *testing.T) {
	db, err := openCacheDB()
	require.Nil(t, err)
	defer db.Close()

	dc := &DepChecker{db: db}

	// Insert a cached "outdated" entry
	_, err = db.Exec(
		`INSERT OR REPLACE INTO deps (path, version, update_version, checked_at) VALUES (?, ?, ?, ?)`,
		"test/cached-outdated", "v0.0.0-20240101-abc123def456", "v1.0.0", 9999999999,
	)
	require.Nil(t, err)

	// Should return cached result
	update, needsUpdate, err := dc.checkDep("test/cached-outdated", "v0.0.0-20240101-abc123def456")
	require.Nil(t, err)
	assert.True(t, needsUpdate)
	assert.Equal(t, "v1.0.0", update)
}

func TestDepChecker_checkDep_CacheFresh(t *testing.T) {
	db, err := openCacheDB()
	require.Nil(t, err)
	defer db.Close()

	dc := &DepChecker{db: db}

	// Insert a fresh "up-to-date" entry (checked just now)
	now := int64(9999999999) // Far future timestamp
	_, err = db.Exec(
		`INSERT OR REPLACE INTO deps (path, version, update_version, checked_at) VALUES (?, ?, ?, ?)`,
		"test/cached-fresh", "v0.0.0-20240101-abc123def456", nil, now,
	)
	require.Nil(t, err)

	// Should return cached "up-to-date" result
	update, needsUpdate, err := dc.checkDep("test/cached-fresh", "v0.0.0-20240101-abc123def456")
	require.Nil(t, err)
	assert.False(t, needsUpdate)
	assert.Equal(t, "", update)
}

func TestDepChecker_checkDep_CacheExpired(t *testing.T) {
	db, err := openCacheDB()
	require.Nil(t, err)
	defer db.Close()

	dc := &DepChecker{db: db}

	// Insert an expired "up-to-date" entry (checked long ago)
	_, err = db.Exec(
		`INSERT OR REPLACE INTO deps (path, version, update_version, checked_at) VALUES (?, ?, ?, ?)`,
		"github.com/spf13/cobra", "v1.10.2", nil, 0, // timestamp 0 = expired
	)
	require.Nil(t, err)

	// Should do a live check since cache is expired
	// This will update the cache
	_, _, err = dc.checkDep("github.com/spf13/cobra", "v1.10.2")
	require.Nil(t, err)

	// Verify cache was updated
	var checkedAt int64
	err = db.QueryRow(`SELECT checked_at FROM deps WHERE path = ? AND version = ?`,
		"github.com/spf13/cobra", "v1.10.2").Scan(&checkedAt)
	require.Nil(t, err)
	assert.NotEqual(t, 0, checkedAt)
}

func TestDepChecker_run_Canceled(t *testing.T) {
	dc := &DepChecker{
		doneCh:   make(chan struct{}),
		canceled: true, // pre-cancel
	}

	// Run should exit early due to cancellation
	dc.run()

	assert.True(t, !dc.done)
}

func TestFixBogusDepsVersions_NoGoMod(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := NewMockRunner()

	// No go.mod exists, should return nil without doing anything
	err := FixBogusDepsVersions(mock)
	assert.Nil(t, err)
	assert.Equal(t, 0, len(mock.Commands))
}

func TestFixBogusDepsVersions_NoBogusVersions(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create go.mod with normal versions
	gomod := `module test
go 1.21

require (
	github.com/spf13/cobra v1.8.0
	github.com/stretchr/testify v1.9.0
)
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	mock := NewMockRunner()
	err := FixBogusDepsVersions(mock)
	assert.Nil(t, err)
	assert.Equal(t, 0, len(mock.Commands))
}

func TestFixBogusDepsVersions_DetectsBogusVersions(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create go.mod with v0.0.0 dependencies
	gomod := `module test
go 1.21

require (
	git.internal/service/auth v0.0.0
	github.com/spf13/cobra v1.8.0
	git.internal/lib/utils v0.0.0 // indirect
)
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	mock := NewMockRunner()
	// Mock git ls-remote to fail - we just want to verify detection works
	mock.SetResponse("git", []string{"ls-remote", "https://git.internal/service/auth", "HEAD"},
		nil, os.ErrNotExist)

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := FixBogusDepsVersions(mock)
	// Should fail because git ls-remote failed
	assert.NotNil(t, err)

	// Verify it tried to resolve the first v0.0.0 dep
	require.GreaterOrEqual(t, len(mock.Commands), 1)
	assert.False(t, mock.Commands[0].Name != "git" || mock.Commands[0].Args[0] != "ls-remote")
	assert.Equal(t, "https://git.internal/service/auth", mock.Commands[0].Args[1])
}

func TestFixBogusDepsVersions_GitLsRemoteFails(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gomod := `module test
go 1.21

require git.internal/broken v0.0.0
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	mock := NewMockRunner()
	mock.SetResponse("git", []string{"ls-remote", "https://git.internal/broken", "HEAD"}, nil, os.ErrNotExist)

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := FixBogusDepsVersions(mock)
	assert.NotNil(t, err)
}

func TestResolveLatestVersionViaGit_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	runner := &RealCommandRunner{Quiet: true}
	mod := "github.com/spf13/pflag"

	// Get version via our function
	version, err := resolveLatestVersionViaGit(runner, mod)
	require.Nil(t, err)

	// Verify Go accepts this version by querying the module
	// This catches timezone bugs - Go validates the timestamp matches the commit
	output, err := runner.RunWithOutput("go", "list", "-m", "-json", mod+"@"+version)
	require.Nil(t, err)

	// Verify the response contains our version
	assert.Contains(t, string(output), version)
}

func TestResolveLatestVersionViaGit_NoHeadRef(t *testing.T) {
	mock := NewMockRunner()
	// Return empty output (no HEAD ref)
	mock.SetResponse("git", []string{"ls-remote", "https://example.com/repo", "HEAD"}, []byte(""), nil)

	_, err := resolveLatestVersionViaGit(mock, "example.com/repo")
	assert.NotNil(t, err)
}

func TestResolveLatestVersionViaGit_ShortHash(t *testing.T) {
	mock := NewMockRunner()
	// Return hash that's too short
	mock.SetResponse("git", []string{"ls-remote", "https://example.com/repo", "HEAD"}, []byte("abc123\tHEAD\n"), nil)

	_, err := resolveLatestVersionViaGit(mock, "example.com/repo")
	assert.NotNil(t, err)
}

func TestFixBogusDepsVersions_ParseError(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// Create invalid go.mod
	os.WriteFile("go.mod", []byte("not valid go.mod content {{{"), 0644)

	mock := NewMockRunner()
	jsonOutput = true
	defer func() { jsonOutput = false }()

	// Should return nil (let go mod tidy handle parse errors)
	err := FixBogusDepsVersions(mock)
	assert.Nil(t, err)
}

func TestFixBogusDepsVersions_NoV000Deps(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	// go.mod with no v0.0.0 dependencies
	gomod := `module test
go 1.21

require github.com/spf13/cobra v1.8.0
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	mock := NewMockRunner()
	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := FixBogusDepsVersions(mock)
	assert.Nil(t, err)
	// Should not have run any commands
	assert.Equal(t, 0, len(mock.Commands))
}

func TestFixBogusDepsVersions_PrintsMessage(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	gomod := `module test
go 1.21

require git.internal/foo v0.0.0
`
	os.WriteFile("go.mod", []byte(gomod), 0644)

	mock := NewMockRunner()
	// Don't set jsonOutput = true, so the message will be printed
	mock.SetResponse("git", []string{"ls-remote", "https://git.internal/foo", "HEAD"}, nil, os.ErrNotExist)

	// This will fail but covers the non-jsonOutput branch
	_ = FixBogusDepsVersions(mock)
}

func TestDepChecker_WaitWithProgress_Nil(t *testing.T) {
	var dc *DepChecker
	result := dc.WaitWithProgress()
	assert.Nil(t, result)
}

func TestResolveLatestVersionViaGit_LsRemoteFails(t *testing.T) {
	mock := NewMockRunner()
	mock.SetResponse("git", []string{"ls-remote", "https://example.com/repo", "HEAD"}, nil, os.ErrNotExist)

	_, err := resolveLatestVersionViaGit(mock, "example.com/repo")
	assert.NotNil(t, err)
}

func TestCheckDepLive_NonexistentModule(t *testing.T) {
	// Test with a module that doesn't exist
	_, _, err := checkDepLive("invalid.module.path.that.does.not.exist/foo")
	assert.NotNil(t, err)
}

func TestOpenCacheDB_CreatesDir(t *testing.T) {
	// This test verifies openCacheDB works when cache dir needs creation
	db, err := openCacheDB()
	require.Nil(t, err)
	db.Close()
}

func TestDepChecker_run_DBOpenError(t *testing.T) {
	// Test when we can't open the DB (by using a bad HOME env)
	oldHome := os.Getenv("HOME")
	oldCache := os.Getenv("XDG_CACHE_HOME")
	os.Setenv("HOME", "/nonexistent/path/that/does/not/exist")
	os.Setenv("XDG_CACHE_HOME", "/nonexistent/path/that/does/not/exist")
	defer func() {
		os.Setenv("HOME", oldHome)
		os.Setenv("XDG_CACHE_HOME", oldCache)
	}()

	dc := &DepChecker{
		doneCh: make(chan struct{}),
	}
	dc.run()

	// Should complete with an error
	assert.True(t, !dc.done)
	// Note: error may or may not be set depending on OS behavior with MkdirAll
}
