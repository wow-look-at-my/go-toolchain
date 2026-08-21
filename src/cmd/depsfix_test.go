package cmd

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

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
	mock.SetResponse("git", []string{"ls-remote", "--symref", "https://git.internal/service/auth", "HEAD"},
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
	assert.Equal(t, "https://git.internal/service/auth", lsRemoteURL(calls[0]))
}

// lsRemoteURL picks the repository out of an ls-remote call. The options in
// front of it move the position: HEAD is asked with --symref.
func lsRemoteURL(cfg runner.Config) string {
	for _, arg := range cfg.Args {
		if strings.HasPrefix(arg, "https://") {
			return arg
		}
	}
	return ""
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
	mock.SetResponse("git", []string{"ls-remote", "--symref", "https://git.internal/broken", "HEAD"}, nil, os.ErrNotExist)

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
	mock.SetResponse("git", []string{"ls-remote", "--symref", "https://example.com/repo", "HEAD"}, []byte(""), nil)

	_, err := resolveLatestVersionViaGit(mock, "example.com/repo")
	assert.NotNil(t, err)
}

func TestResolveLatestVersionViaGit_ShortHash(t *testing.T) {
	mock := runner.NewMock()
	// Return hash that's too short
	mock.SetResponse("git", []string{"ls-remote", "--symref", "https://example.com/repo", "HEAD"}, []byte("abc123\tHEAD\n"), nil)

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
	mock.SetResponse("git", []string{"ls-remote", "--symref", "https://git.internal/foo", "HEAD"}, nil, os.ErrNotExist)

	// This will fail but covers the non-jsonOutput branch
	_ = FixBogusDepsVersions(mock)
}

func TestResolveLatestVersionViaGit_LsRemoteFails(t *testing.T) {
	mock := runner.NewMock()
	mock.SetResponse("git", []string{"ls-remote", "--symref", "https://example.com/repo", "HEAD"}, nil, os.ErrNotExist)

	_, err := resolveLatestVersionViaGit(mock, "example.com/repo")
	assert.NotNil(t, err)
}

// TestResolveLatestVersionViaGit_LsRemoteFails_ReportsTheRealError guards the
// error-wrapping bug this exact scenario shipped with: mock.SetResponse's err
// surfaces from the PROCESS's Wait(), not from Run() itself (see mock.go) --
// exactly how a real failing git subprocess behaves. The buggy code wrapped
// the stale, already-nil err from Run() instead, so every one of these
// failures rendered as the meaningless "git ls-remote failed: %!w(<nil>)"
// instead of naming what actually went wrong.
func TestResolveLatestVersionViaGit_LsRemoteFails_ReportsTheRealError(t *testing.T) {
	mock := runner.NewMock()
	mock.SetResponse("git", []string{"ls-remote", "--symref", "https://example.com/repo", "HEAD"}, nil, os.ErrNotExist)

	_, err := resolveLatestVersionViaGit(mock, "example.com/repo")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist, "the real subprocess error must survive, not render as %!w(<nil>)")
}

func TestResolveGitURLAndRef(t *testing.T) {
	t.Run("module at repo root resolves on the first try", func(t *testing.T) {
		mock := runner.NewMock()
		mock.SetResponse("git", []string{"ls-remote", "--symref", "https://github.com/wow-look-at-my/agentic-loop", "HEAD"},
			[]byte("abc123\tHEAD\n"), nil)

		url, output, err := resolveGitURLAndRef(mock, "github.com/wow-look-at-my/agentic-loop", "HEAD")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/wow-look-at-my/agentic-loop", url)
		assert.Contains(t, string(output), "abc123")
		assert.Len(t, mock.Calls(), 1, "the repo-root case must not try any shorter prefix")
	})

	t.Run("module in a subdirectory backs off to the repo root", func(t *testing.T) {
		mock := runner.NewMock()
		mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
			require.True(t, cfg.IsCmd("git", "ls-remote"))
			switch lsRemoteURL(cfg) {
			case "https://github.com/wow-look-at-my/agentic-loop/go":
				return runner.MockProcess(nil, errors.New("exit status 128")), nil
			case "https://github.com/wow-look-at-my/agentic-loop":
				return runner.MockProcess([]byte("abc123\tHEAD\n"), nil), nil
			}
			t.Fatalf("unexpected ls-remote URL %q", lsRemoteURL(cfg))
			return nil, nil
		}

		url, _, err := resolveGitURLAndRef(mock, "github.com/wow-look-at-my/agentic-loop/go", "HEAD")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/wow-look-at-my/agentic-loop", url)
	})

	// No hardcoded host table means a nested GitLab subgroup resolves the
	// same way a GitHub owner/repo does: the deepest prefix that IS a real
	// repository wins, found by trying, not by knowing GitLab's shape.
	t.Run("a nested gitlab subgroup resolves without knowing gitlab's shape in advance", func(t *testing.T) {
		mock := runner.NewMock()
		mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
			switch lsRemoteURL(cfg) {
			case "https://gitlab.com/group/subgroup/repo/sub":
				return runner.MockProcess(nil, errors.New("exit status 128")), nil
			case "https://gitlab.com/group/subgroup/repo":
				return runner.MockProcess([]byte("abc123\tHEAD\n"), nil), nil
			}
			t.Fatalf("unexpected ls-remote URL %q", lsRemoteURL(cfg))
			return nil, nil
		}

		url, _, err := resolveGitURLAndRef(mock, "gitlab.com/group/subgroup/repo/sub", "HEAD")
		require.NoError(t, err)
		assert.Equal(t, "https://gitlab.com/group/subgroup/repo", url)
	})

	t.Run("a repo that exists but lacks the ref stops immediately instead of guessing shorter prefixes", func(t *testing.T) {
		mock := runner.NewMock()
		mock.SetResponse("git", []string{"ls-remote", "https://github.com/owner/repo", "refs/heads/nonexistent"},
			[]byte(""), nil)

		url, output, err := resolveGitURLAndRef(mock, "github.com/owner/repo", "refs/heads/nonexistent")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/owner/repo", url)
		assert.Empty(t, output)
		assert.Len(t, mock.Calls(), 1, "a successful-but-empty result must not trigger backoff")
	})

	t.Run("no prefix resolves", func(t *testing.T) {
		mock := runner.NewMock()
		mock.Handler = func(runner.Config) (runner.IProcess, error) {
			return runner.MockProcess(nil, errors.New("exit status 128")), nil
		}

		_, _, err := resolveGitURLAndRef(mock, "example.com/a/b/c", "HEAD")
		assert.Error(t, err)
	})
}

// TestResolveVersionViaGit_SubdirectoryModule is the exact reported failure:
// github.com/wow-look-at-my/agentic-loop/go, a Go module living in the go/
// subdirectory of the agentic-loop repo. Before the fix, ls-remote and fetch
// were both issued against the unclonable
// "https://github.com/wow-look-at-my/agentic-loop/go".
func TestResolveVersionViaGit_SubdirectoryModule(t *testing.T) {
	const mod = "github.com/wow-look-at-my/agentic-loop/go"
	const repoRoot = "https://github.com/wow-look-at-my/agentic-loop"
	fullHash := "82fff4e9411d179f66b298a6549311698a096122"

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			if cfg.Args[1] == "https://"+mod {
				// The full import path is not a repository -- the exact 403
				// this bug reproduced.
				return runner.MockProcess(nil, errors.New("exit status 128")), nil
			}
			require.Equal(t, repoRoot, cfg.Args[1], "ls-remote must fall back to the repo root")
			return runner.MockProcess([]byte(fullHash+"\trefs/heads/master\n"), nil), nil
		}
		if cfg.IsCmd("git", "fetch") {
			require.Equal(t, repoRoot, cfg.Args[3], "fetch must target the repo root too")
			return runner.MockProcess(nil, nil), nil
		}
		for _, arg := range cfg.Args {
			if arg == "init" {
				return runner.MockProcess(nil, nil), nil
			}
			if arg == "log" {
				return runner.MockProcess([]byte("1754706071\n"), nil), nil
			}
		}
		return nil, nil
	}

	version, err := resolveVersionViaGit(mock, mod, "refs/heads/master")
	require.NoError(t, err)
	assert.Contains(t, version, fullHash[:12])
}

// A "/vN" module path demands a matching vN pseudo-version: the go command
// rejects a v0 one with `go.mod has post-v0 module path "..." at revision`,
// so every branch-tracked v2+ module got an unresolvable pin.
func TestPseudoVersionForDerivesTheMajorFromTheModulePath(t *testing.T) {
	// Measured: go list -m github.com/wow-look-at-my/bubbletea/v2@master.
	assert.Equal(t, "v2.0.0-20260812203640-351d2159f8d8", pseudoVersionFor(
		"github.com/wow-look-at-my/bubbletea/v2",
		time.Unix(1786567000, 0), "351d2159f8d8"))

	assert.Equal(t, "v0.0.0-20260812203640-351d2159f8d8", pseudoVersionFor(
		"github.com/wow-look-at-my/bubbletea",
		time.Unix(1786567000, 0), "351d2159f8d8"))

	assert.Equal(t, "v11.0.0-20260812203640-351d2159f8d8", pseudoVersionFor(
		"example.com/foo/v11", time.Unix(1786567000, 0), "351d2159f8d8"))

	// gopkg.in spells its major with a dot and allows every N.
	assert.Equal(t, "v2.0.0-20260812203640-351d2159f8d8", pseudoVersionFor(
		"gopkg.in/yaml.v2", time.Unix(1786567000, 0), "351d2159f8d8"))
}

func TestResolveVersionViaGitCarriesTheMajorThrough(t *testing.T) {
	const fullHash = "351d2159f8d8a85613aa2a6e98c8c63df3c98623"
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			return runner.MockProcess([]byte(fullHash+"\trefs/heads/master\n"), nil), nil
		}
		for _, arg := range cfg.Args {
			if arg == "init" || arg == "fetch" {
				return runner.MockProcess(nil, nil), nil
			}
			if arg == "log" {
				return runner.MockProcess([]byte("1786567000\n"), nil), nil
			}
		}
		return nil, nil
	}

	version, err := resolveVersionViaGit(mock, "github.com/wow-look-at-my/bubbletea/v2", "refs/heads/master")
	require.NoError(t, err)
	assert.Equal(t, "v2.0.0-20260812203640-351d2159f8d8", version)
}
