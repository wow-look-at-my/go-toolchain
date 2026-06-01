package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// withEmptyEnviron replaces environFunc with one that returns no entries,
// ensuring the env-scan path doesn't pick up real tokens from the test host.
func withEmptyEnviron(t *testing.T) {
	t.Helper()
	old := environFunc
	environFunc = func() []string { return nil }
	t.Cleanup(func() { environFunc = old })
}

// enableTokenSearch sets the gate env var so discoverGitHubToken runs.
func enableTokenSearch(t *testing.T) {
	t.Helper()
	t.Setenv("GO_TOOLCHAIN_AGGRESSIVE_TOKEN_SEARCH", "1")
}

func TestDiscoverGitHubToken_DisabledByDefault(t *testing.T) {
	for _, val := range []string{"", "0", "false", "False", "no", "off", "OFF"} {
		t.Run("val="+val, func(t *testing.T) {
			t.Setenv("GO_TOOLCHAIN_AGGRESSIVE_TOKEN_SEARCH", val)
			t.Setenv("GITHUB_TOKEN", "ghp_should_not_find_this")
			got := discoverGitHubToken()
			assert.Equal(t, "", got)
		})
	}
}

func TestDiscoverGitHubToken_WellKnownVars(t *testing.T) {
	enableTokenSearch(t)
	withEmptyEnviron(t)
	for _, name := range wellKnownTokenVars {
		t.Run(name, func(t *testing.T) {
			enableTokenSearch(t)
			// Clear all well-known vars first.
			for _, n := range wellKnownTokenVars {
				t.Setenv(n, "")
			}
			t.Setenv(name, "test-token-value")
			got := discoverGitHubToken()
			assert.Equal(t, "test-token-value", got)
		})
	}
}

func TestDiscoverGitHubToken_Priority(t *testing.T) {
	enableTokenSearch(t)
	withEmptyEnviron(t)
	// GITHUB_TOKEN should take priority over GH_TOKEN.
	t.Setenv("GITHUB_TOKEN", "first")
	t.Setenv("GH_TOKEN", "second")
	got := discoverGitHubToken()
	assert.Equal(t, "first", got)
}

func TestDiscoverGitHubToken_ScansEnvForPAT(t *testing.T) {
	enableTokenSearch(t)
	// Clear well-known vars.
	for _, n := range wellKnownTokenVars {
		t.Setenv(n, "")
	}
	// Override environFunc to return only our custom var.
	old := environFunc
	environFunc = func() []string {
		return []string{"MY_CUSTOM_VAR=ghp_abc123def456"}
	}
	t.Cleanup(func() { environFunc = old })

	got := discoverGitHubToken()
	assert.Equal(t, "ghp_abc123def456", got)
}

func TestDiscoverGitHubToken_FineGrainedPAT(t *testing.T) {
	enableTokenSearch(t)
	for _, n := range wellKnownTokenVars {
		t.Setenv(n, "")
	}
	old := environFunc
	environFunc = func() []string {
		return []string{"SOME_TOKEN=github_pat_xxxxxxxxxxxx"}
	}
	t.Cleanup(func() { environFunc = old })

	got := discoverGitHubToken()
	assert.Equal(t, "github_pat_xxxxxxxxxxxx", got)
}

func TestDiscoverGitHubToken_NoToken(t *testing.T) {
	enableTokenSearch(t)
	for _, n := range wellKnownTokenVars {
		t.Setenv(n, "")
	}
	old := environFunc
	environFunc = func() []string { return nil }
	t.Cleanup(func() { environFunc = old })

	got := discoverGitHubToken()
	assert.Equal(t, "", got)
}

func TestNewGitHubRequest_NoToken(t *testing.T) {
	t.Setenv("GO_TOOLCHAIN_AGGRESSIVE_TOKEN_SEARCH", "")
	for _, n := range wellKnownTokenVars {
		t.Setenv(n, "")
	}
	old := environFunc
	environFunc = func() []string { return nil }
	t.Cleanup(func() { environFunc = old })

	req, err := newGitHubRequest("https://api.github.com/repos/test/test")
	assert.Nil(t, err)
	assert.Equal(t, "", req.Header.Get("Authorization"))
}

func TestNewGitHubRequest_WithToken(t *testing.T) {
	enableTokenSearch(t)
	withEmptyEnviron(t)
	t.Setenv("GITHUB_TOKEN", "ghp_testtoken123")
	req, err := newGitHubRequest("https://api.github.com/repos/test/test")
	assert.Nil(t, err)
	assert.Equal(t, "token ghp_testtoken123", req.Header.Get("Authorization"))
}
