package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestFixBogusDepsVersions_NoGoMod(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := runner.NewMock()

	// No go.mod exists, should return nil without doing anything
	err := FixBogusDepsVersions(mock)
	assert.Nil(t, err)
	assert.Equal(t, 0, len(mock.Calls()))
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

	mock := runner.NewMock()
	err := FixBogusDepsVersions(mock)
	assert.Nil(t, err)
	assert.Equal(t, 0, len(mock.Calls()))
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

	mock := runner.NewMock()
	// Mock git ls-remote to fail - we just want to verify detection works
	mock.SetResponse("git", []string{"ls-remote", "https://git.internal/service/auth", "HEAD"},
		nil, os.ErrNotExist)

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := FixBogusDepsVersions(mock)
	// Should fail because git ls-remote failed
	assert.NotNil(t, err)

	// Verify it tried to resolve the first v0.0.0 dep
	calls := mock.Calls()
	require.GreaterOrEqual(t, len(calls), 1)
	assert.False(t, calls[0].Name != "git" || calls[0].Args[0] != "ls-remote")
	assert.Equal(t, "https://git.internal/service/auth", calls[0].Args[1])
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

	mock := runner.NewMock()
	mock.SetResponse("git", []string{"ls-remote", "https://git.internal/broken", "HEAD"}, nil, os.ErrNotExist)

	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := FixBogusDepsVersions(mock)
	assert.NotNil(t, err)
}

func TestResolveLatestVersionViaGit_Success(t *testing.T) {
	fullHash := "abc123def456789012345678901234567890abcd"

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			return runner.MockProcess([]byte(fullHash+"\tHEAD\n"), nil), nil
		}
		if cfg.IsCmd("git") {
			// init --bare, fetch, log
			for _, arg := range cfg.Args {
				if arg == "init" || arg == "fetch" {
					return runner.MockProcess(nil, nil), nil
				}
				if arg == "log" {
					// Return a Unix timestamp
					return runner.MockProcess([]byte("1700000000\n"), nil), nil
				}
			}
		}
		return nil, nil
	}

	version, err := resolveLatestVersionViaGit(mock, "example.com/repo")
	require.Nil(t, err)
	assert.Contains(t, version, "v0.0.0-")
	assert.Contains(t, version, fullHash[:12])
}

func TestResolveLatestVersionViaGit_NoHeadRef(t *testing.T) {
	mock := runner.NewMock()
	// Return empty output (no HEAD ref)
	mock.SetResponse("git", []string{"ls-remote", "https://example.com/repo", "HEAD"}, []byte(""), nil)

	_, err := resolveLatestVersionViaGit(mock, "example.com/repo")
	assert.NotNil(t, err)
}

func TestResolveLatestVersionViaGit_ShortHash(t *testing.T) {
	mock := runner.NewMock()
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

	mock := runner.NewMock()
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

	mock := runner.NewMock()
	jsonOutput = true
	defer func() { jsonOutput = false }()

	err := FixBogusDepsVersions(mock)
	assert.Nil(t, err)
	// Should not have run any commands
	assert.Equal(t, 0, len(mock.Calls()))
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

	mock := runner.NewMock()
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
	mock := runner.NewMock()
	mock.SetResponse("git", []string{"ls-remote", "https://example.com/repo", "HEAD"}, nil, os.ErrNotExist)

	_, err := resolveLatestVersionViaGit(mock, "example.com/repo")
	assert.NotNil(t, err)
}

func TestCheckDepLive_NonexistentModule(t *testing.T) {
	// Test with a module that doesn't exist
	_, _, err := checkDepLive("invalid.module.path.that.does.not.exist/foo")
	assert.NotNil(t, err)
}

func TestOpenDepsCache_CreatesDir(t *testing.T) {
	// This test verifies openDepsCache works when the cache dir needs creation
	c, err := openDepsCache()
	require.Nil(t, err)
	c.close()
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

	// Should complete (done=true) with an error
	assert.True(t, dc.done)
	// Note: error may or may not be set depending on OS behavior with MkdirAll
}
