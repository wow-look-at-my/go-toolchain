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

func TestResolveLatestVersionViaGit_LsRemoteFails(t *testing.T) {
	mock := runner.NewMock()
	mock.SetResponse("git", []string{"ls-remote", "https://example.com/repo", "HEAD"}, nil, os.ErrNotExist)

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
	mock.SetResponse("git", []string{"ls-remote", "https://example.com/repo", "HEAD"}, nil, os.ErrNotExist)

	_, err := resolveLatestVersionViaGit(mock, "example.com/repo")
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist, "the real subprocess error must survive, not render as %!w(<nil>)")
}

func TestGitRemoteURL(t *testing.T) {
	cases := []struct {
		name string
		mod  string
		want string
	}{
		{
			name: "github module at repo root",
			mod:  "github.com/wow-look-at-my/agentic-loop",
			want: "https://github.com/wow-look-at-my/agentic-loop",
		},
		{
			name: "github module in a subdirectory of its repo",
			mod:  "github.com/wow-look-at-my/agentic-loop/go",
			want: "https://github.com/wow-look-at-my/agentic-loop",
		},
		{
			name: "github module several directories deep",
			mod:  "github.com/wow-look-at-my/agentic-loop/go/internal",
			want: "https://github.com/wow-look-at-my/agentic-loop",
		},
		{
			name: "bitbucket module in a subdirectory",
			mod:  "bitbucket.org/owner/repo/sub",
			want: "https://bitbucket.org/owner/repo",
		},
		{
			name: "gitlab is left untouched -- nested subgroups are indistinguishable from a module subdirectory",
			mod:  "gitlab.com/group/subgroup/repo",
			want: "https://gitlab.com/group/subgroup/repo",
		},
		{
			name: "an unrecognized host is passed through unchanged",
			mod:  "example.com/owner/repo/sub",
			want: "https://example.com/owner/repo/sub",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, gitRemoteURL(c.mod))
		})
	}
}

// TestResolveVersionViaGit_SubdirectoryModule is the exact reported failure:
// github.com/wow-look-at-my/agentic-loop/go, a Go module living in the go/
// subdirectory of the agentic-loop repo. Before the fix, ls-remote and fetch
// were both issued against the unclonable
// "https://github.com/wow-look-at-my/agentic-loop/go".
func TestResolveVersionViaGit_SubdirectoryModule(t *testing.T) {
	const mod = "github.com/wow-look-at-my/agentic-loop/go"
	fullHash := "82fff4e9411d179f66b298a6549311698a096122"

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("git", "ls-remote") {
			require.Equal(t, "https://github.com/wow-look-at-my/agentic-loop", cfg.Args[1],
				"ls-remote must target the repo root, not the module's subdirectory")
			return runner.MockProcess([]byte(fullHash+"\trefs/heads/master\n"), nil), nil
		}
		if cfg.IsCmd("git", "fetch") {
			require.Equal(t, "https://github.com/wow-look-at-my/agentic-loop", cfg.Args[3],
				"fetch must target the repo root too")
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
