package cmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
	assert.True(t, dc.canceled)
}

func TestCheckOutdatedDeps(t *testing.T) {
	// This test verifies the function doesn't panic and returns a DepChecker.
	// We cancel immediately rather than waiting for completion, since the live
	// dependency checks require network access and can exceed the test timeout.
	dc := CheckOutdatedDeps()
	assert.NotNil(t, dc)
	dc.Cancel()
	<-dc.doneCh
	assert.True(t, dc.Done())
}

func TestOpenDepsCache(t *testing.T) {
	c, err := openDepsCache()
	require.Nil(t, err)
	defer c.close()

	// Verify the backing store works with a store+lookup round-trip
	c.store("test/module", "v1.0.0", "", 12345)

	update, checkedAt, found := c.lookup("test/module", "v1.0.0")
	assert.True(t, found)
	assert.Equal(t, "", update)
	assert.Equal(t, int64(12345), checkedAt)
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

func TestCheckDepLive_WithUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"Version":"v1.2.0"}`)
	}))
	defer srv.Close()
	t.Setenv("GOPROXY", srv.URL)

	update, needsUpdate, err := checkDepLive("github.com/spf13/cobra")
	require.Nil(t, err)
	assert.True(t, needsUpdate)
	assert.Equal(t, "v1.2.0", update)
}

func TestCheckDepLive_NoUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{}`)
	}))
	defer srv.Close()
	t.Setenv("GOPROXY", srv.URL)

	update, needsUpdate, err := checkDepLive("github.com/spf13/cobra")
	require.Nil(t, err)
	assert.False(t, needsUpdate)
	assert.Equal(t, "", update)
}

func TestCheckDepLive_ProxyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	t.Setenv("GOPROXY", srv.URL)

	_, _, err := checkDepLive("github.com/fake/module")
	assert.NotNil(t, err)
}

func TestCheckDepLive_NoProxy(t *testing.T) {
	t.Setenv("GOPROXY", "direct")
	_, _, err := checkDepLive("github.com/spf13/cobra")
	assert.NotNil(t, err)
}

func TestDepChecker_checkDep_CacheHit(t *testing.T) {
	c, err := openDepsCache()
	require.Nil(t, err)
	defer c.close()

	dc := &DepChecker{cache: c}

	// Insert a cached "outdated" entry
	c.store("test/cached-outdated", "v0.0.0-20240101-abc123def456", "v1.0.0", 9999999999)

	// Should return cached result
	update, needsUpdate, err := dc.checkDep("test/cached-outdated", "v0.0.0-20240101-abc123def456")
	require.Nil(t, err)
	assert.True(t, needsUpdate)
	assert.Equal(t, "v1.0.0", update)
}

func TestDepChecker_checkDep_CacheFresh(t *testing.T) {
	c, err := openDepsCache()
	require.Nil(t, err)
	defer c.close()

	dc := &DepChecker{cache: c}

	// Insert a fresh "up-to-date" entry (checked just now)
	now := int64(9999999999) // Far future timestamp
	c.store("test/cached-fresh", "v0.0.0-20240101-abc123def456", "", now)

	// Should return cached "up-to-date" result
	update, needsUpdate, err := dc.checkDep("test/cached-fresh", "v0.0.0-20240101-abc123def456")
	require.Nil(t, err)
	assert.False(t, needsUpdate)
	assert.Equal(t, "", update)
}

func TestDepChecker_checkDep_CacheExpired(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, `{"Version":"v1.11.0"}`)
	}))
	defer srv.Close()
	t.Setenv("GOPROXY", srv.URL)

	c, err := openDepsCache()
	require.Nil(t, err)
	defer c.close()

	dc := &DepChecker{cache: c}

	// Insert an expired "up-to-date" entry (checked long ago)
	c.store("github.com/spf13/cobra", "v1.10.2", "", 0) // timestamp 0 = expired

	// Should do a live check since cache is expired
	_, _, err = dc.checkDep("github.com/spf13/cobra", "v1.10.2")
	require.Nil(t, err)

	// Verify cache was updated
	_, checkedAt, found := c.lookup("github.com/spf13/cobra", "v1.10.2")
	require.True(t, found)
	assert.NotEqual(t, int64(0), checkedAt)
}

func TestDepChecker_run_Canceled(t *testing.T) {
	dc := &DepChecker{
		doneCh:   make(chan struct{}),
		canceled: true, // pre-cancel
	}

	// Run should exit early due to cancellation
	dc.run()

	assert.True(t, dc.done)
}
