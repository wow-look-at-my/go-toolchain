package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestGenerateReleaseNotes(t *testing.T) {
	commits := []string{"add feature X", "fix bug Y"}
	checksums := "abc123  tool_linux_amd64\ndef456  tool_darwin_arm64\n"

	notes := generateReleaseNotes("v1.0.0", commits, checksums)

	assert.Contains(t, notes, "## What's Changed")
	assert.Contains(t, notes, "- add feature X")
	assert.Contains(t, notes, "- fix bug Y")
	assert.Contains(t, notes, "## Checksums")
	assert.Contains(t, notes, "abc123  tool_linux_amd64")
	assert.Contains(t, notes, "## Verification")
	assert.Contains(t, notes, "cosign verify-blob")
}

func TestGenerateReleaseNotesNoChecksums(t *testing.T) {
	commits := []string{"initial commit"}
	notes := generateReleaseNotes("v0.1.0", commits, "")

	assert.Contains(t, notes, "## What's Changed")
	assert.Contains(t, notes, "- initial commit")
	assert.NotContains(t, notes, "## Checksums")
	assert.Contains(t, notes, "## Verification")
}

func TestGenerateReleaseNotesNoCommits(t *testing.T) {
	notes := generateReleaseNotes("v0.0.1", nil, "")

	assert.Contains(t, notes, "- Initial release")
}

func TestParseCommitLines(t *testing.T) {
	input := "abc1234 add feature X\ndef5678 fix bug Y\n"
	commits := parseCommitLines(input)
	assert.Equal(t, []string{"add feature X", "fix bug Y"}, commits)
}

func TestParseCommitLinesEmpty(t *testing.T) {
	commits := parseCommitLines("")
	assert.Nil(t, commits)
}

func TestParseCommitLinesNoSpace(t *testing.T) {
	// A hash with no message (edge case)
	commits := parseCommitLines("abc1234")
	assert.Equal(t, []string{"abc1234"}, commits)
}

// mockExecutor implements releaseExecutor for testing.
type mockExecutor struct {
	gitOutputCalls	[][]string
	gitRunCalls	[][]string
	ghReleaseCalls	[][]string

	gitOutputFunc	func(args ...string) (string, error)
	gitRunFunc	func(args ...string) error
	ghReleaseFunc	func(args ...string) error
}

func (m *mockExecutor) gitOutput(args ...string) (string, error) {
	m.gitOutputCalls = append(m.gitOutputCalls, args)
	if m.gitOutputFunc != nil {
		return m.gitOutputFunc(args...)
	}
	return "", nil
}

func (m *mockExecutor) gitRun(args ...string) error {
	m.gitRunCalls = append(m.gitRunCalls, args)
	if m.gitRunFunc != nil {
		return m.gitRunFunc(args...)
	}
	return nil
}

func (m *mockExecutor) ghRelease(args ...string) error {
	m.ghReleaseCalls = append(m.ghReleaseCalls, args)
	if m.ghReleaseFunc != nil {
		return m.ghReleaseFunc(args...)
	}
	return nil
}

func TestReleaseCmdAbort(t *testing.T) {
	t.Setenv("CI", "")
	oldTag := releaseTag
	releaseTag = "v1.0.0"
	defer func() { releaseTag = oldTag }()

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 test commit", nil
			}
			return "", fmt.Errorf("no previous tag")
		},
	}
	stdin := strings.NewReader("n\n")
	err := runReleaseCmdImpl(stdin, mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "release aborted")
}

func TestReleaseCmdAbortEmpty(t *testing.T) {
	t.Setenv("CI", "")
	oldTag := releaseTag
	releaseTag = "v1.0.0"
	defer func() { releaseTag = oldTag }()

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 test commit", nil
			}
			return "", fmt.Errorf("no previous tag")
		},
	}
	stdin := strings.NewReader("\n")
	err := runReleaseCmdImpl(stdin, mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "release aborted")
}

func TestReleaseCmdSuccess(t *testing.T) {
	t.Setenv("CI", "true")

	tmpDir := t.TempDir()
	oldOutput := outputDir
	oldTag := releaseTag
	oldFrom := releaseFrom
	outputDir = tmpDir
	releaseTag = "v1.0.0"
	releaseFrom = "v0.9.0"
	defer func() {
		outputDir = oldOutput
		releaseTag = oldTag
		releaseFrom = oldFrom
	}()

	// Create a fake binary and checksums file
	os.WriteFile(filepath.Join(tmpDir, "go-toolchain_linux_amd64"), []byte("binary"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "checksums.txt"), []byte("abc123  go-toolchain_linux_amd64\n"), 0644)

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 add feature\ndef5678 fix bug", nil
			}
			return "", nil
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.Nil(t, err)

	// Verify git tag and push were called
	assert.Equal(t, 5, len(mock.gitRunCalls))
	assert.Equal(t, []string{"tag", "v1.0.0"}, mock.gitRunCalls[0])
	assert.Equal(t, []string{"push", "origin", "v1.0.0"}, mock.gitRunCalls[1])
	assert.Equal(t, []string{"tag", "-f", "master", "HEAD"}, mock.gitRunCalls[2])
	assert.Equal(t, []string{"tag", "-f", "latest", "HEAD"}, mock.gitRunCalls[3])
	assert.Equal(t, []string{"push", "-f", "origin", "refs/tags/master", "refs/tags/latest"}, mock.gitRunCalls[4])

	// Verify gh release create was called with the binary and checksums
	assert.Equal(t, 1, len(mock.ghReleaseCalls))
	ghArgs := mock.ghReleaseCalls[0]
	assert.Equal(t, "create", ghArgs[0])
	assert.Equal(t, "v1.0.0", ghArgs[1])
	// Should contain the binary and checksums.txt
	argsStr := strings.Join(ghArgs, " ")
	assert.Contains(t, argsStr, "go-toolchain_linux_amd64")
	assert.Contains(t, argsStr, "checksums.txt")
	assert.Contains(t, argsStr, "--notes-file")
}

func TestReleaseCmdAutoTag(t *testing.T) {
	t.Setenv("CI", "true")

	tmpDir := t.TempDir()
	oldOutput := outputDir
	oldTag := releaseTag
	oldFrom := releaseFrom
	outputDir = tmpDir
	releaseTag = ""	// auto-detect
	releaseFrom = ""
	defer func() {
		outputDir = oldOutput
		releaseTag = oldTag
		releaseFrom = oldFrom
	}()

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) >= 2 && args[0] == "describe" && args[1] == "--tags" {
				if len(args) > 2 && args[2] == "--abbrev=0" {
					return "v0.9.0", nil
				}
				return "v1.0.0-3-gabc1234", nil
			}
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 commit one", nil
			}
			return "", nil
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.Nil(t, err)

	// Verify the auto-detected tag was used
	assert.Equal(t, []string{"tag", "v1.0.0-3-gabc1234"}, mock.gitRunCalls[0])
}

func TestReleaseCmdGitTagFails(t *testing.T) {
	t.Setenv("CI", "true")
	oldTag := releaseTag
	releaseTag = "v1.0.0"
	defer func() { releaseTag = oldTag }()

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 commit", nil
			}
			return "", fmt.Errorf("not found")
		},
		gitRunFunc: func(args ...string) error {
			if len(args) > 0 && args[0] == "tag" {
				return fmt.Errorf("tag already exists")
			}
			return nil
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to create tag")
}

func TestReleaseCmdGhReleaseFails(t *testing.T) {
	t.Setenv("CI", "true")
	oldTag := releaseTag
	releaseTag = "v1.0.0"
	defer func() { releaseTag = oldTag }()

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 commit", nil
			}
			return "", fmt.Errorf("not found")
		},
		ghReleaseFunc: func(args ...string) error {
			return fmt.Errorf("gh auth required")
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "gh release create failed")
}

func TestReleaseCmdSkipsSymlinks(t *testing.T) {
	t.Setenv("CI", "true")

	tmpDir := t.TempDir()
	oldOutput := outputDir
	oldTag := releaseTag
	outputDir = tmpDir
	releaseTag = "v1.0.0"
	defer func() {
		outputDir = oldOutput
		releaseTag = oldTag
	}()

	// Create a real binary and a symlink
	os.WriteFile(filepath.Join(tmpDir, "go-toolchain_linux_amd64"), []byte("binary"), 0755)
	os.Symlink("go-toolchain_linux_amd64", filepath.Join(tmpDir, "go-toolchain_host"))

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 commit", nil
			}
			return "", fmt.Errorf("not found")
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.Nil(t, err)

	// Verify symlink was excluded from gh release args
	ghArgs := mock.ghReleaseCalls[0]
	argsStr := strings.Join(ghArgs, " ")
	assert.Contains(t, argsStr, "go-toolchain_linux_amd64")
	assert.NotContains(t, argsStr, "go-toolchain_host")
}

func TestReleaseCmdPushTagFails(t *testing.T) {
	t.Setenv("CI", "true")
	oldTag := releaseTag
	releaseTag = "v1.0.0"
	defer func() { releaseTag = oldTag }()

	callCount := 0
	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 commit", nil
			}
			return "", fmt.Errorf("not found")
		},
		gitRunFunc: func(args ...string) error {
			callCount++
			if callCount == 2 {	// push origin <tag>
				return fmt.Errorf("push rejected")
			}
			return nil
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to push tag")
}

func TestReleaseCmdAutoTagFails(t *testing.T) {
	t.Setenv("CI", "true")
	oldTag := releaseTag
	releaseTag = ""	// auto-detect
	defer func() { releaseTag = oldTag }()

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			return "", fmt.Errorf("not a git repo")
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to determine tag")
}

func TestReleaseCmdCollectCommitsFails(t *testing.T) {
	t.Setenv("CI", "true")
	oldTag := releaseTag
	releaseTag = "v1.0.0"
	defer func() { releaseTag = oldTag }()

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			return "", fmt.Errorf("git failed")
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to collect commits")
}

func TestCollectCommitsWithExecutor(t *testing.T) {
	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			// Verify the range is passed
			for _, a := range args {
				if a == "v0.9.0..HEAD" {
					return "abc1234 first commit\ndef5678 second commit", nil
				}
			}
			return "abc1234 all commits", nil
		},
	}

	commits, err := collectCommitsWithExecutor("v0.9.0", mock)
	assert.Nil(t, err)
	assert.Equal(t, []string{"first commit", "second commit"}, commits)
}

func TestCollectCommitsWithExecutorNoFrom(t *testing.T) {
	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			// Should not contain a range
			for _, a := range args {
				assert.NotContains(t, a, "..")

			}
			return "abc1234 initial commit", nil
		},
	}

	commits, err := collectCommitsWithExecutor("", mock)
	assert.Nil(t, err)
	assert.Equal(t, []string{"initial commit"}, commits)
}

func TestReleaseCmdWithChecksums(t *testing.T) {
	t.Setenv("CI", "true")

	tmpDir := t.TempDir()
	oldOutput := outputDir
	oldTag := releaseTag
	outputDir = tmpDir
	releaseTag = "v1.0.0"
	defer func() {
		outputDir = oldOutput
		releaseTag = oldTag
	}()

	// Create checksums + signature files
	os.WriteFile(filepath.Join(tmpDir, "checksums.txt"), []byte("abc  bin\n"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "checksums.txt.sig"), []byte("sig"), 0644)
	os.WriteFile(filepath.Join(tmpDir, "checksums.txt.pem"), []byte("cert"), 0644)

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 commit", nil
			}
			return "", fmt.Errorf("not found")
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.Nil(t, err)

	// All three checksum files should be in gh args
	ghArgs := strings.Join(mock.ghReleaseCalls[0], " ")
	assert.Contains(t, ghArgs, "checksums.txt")
	assert.Contains(t, ghArgs, "checksums.txt.sig")
	assert.Contains(t, ghArgs, "checksums.txt.pem")
}

// TestReleaseCmdIncludesNonToolchainBinaries is a regression test for a bug
// where release artifacts were globbed with a hardcoded "go-toolchain_*"
// prefix, causing releases from consuming modules (whose binaries are named
// after their own module, e.g. "test-server_linux_amd64") to contain zero
// binaries.
func TestReleaseCmdIncludesNonToolchainBinaries(t *testing.T) {
	t.Setenv("CI", "true")

	tmpDir := t.TempDir()
	oldOutput := outputDir
	oldTag := releaseTag
	outputDir = tmpDir
	releaseTag = "v1.0.0"
	defer func() {
		outputDir = oldOutput
		releaseTag = oldTag
	}()

	// Simulate a consuming repo whose binary is named after its own module,
	// not "go-toolchain".
	os.WriteFile(filepath.Join(tmpDir, "test-server_linux_amd64"), []byte("binary"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "test-server_darwin_arm64"), []byte("binary"), 0755)
	os.WriteFile(filepath.Join(tmpDir, "checksums.txt"), []byte("abc  test-server_linux_amd64\n"), 0644)

	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 commit", nil
			}
			return "", fmt.Errorf("not found")
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.Nil(t, err)

	ghArgs := strings.Join(mock.ghReleaseCalls[0], " ")
	assert.Contains(t, ghArgs, "test-server_linux_amd64")
	assert.Contains(t, ghArgs, "test-server_darwin_arm64")
	assert.Contains(t, ghArgs, "checksums.txt")
}

func TestReleaseCmdRollingTagFails(t *testing.T) {
	t.Setenv("CI", "true")
	oldTag := releaseTag
	releaseTag = "v1.0.0"
	defer func() { releaseTag = oldTag }()

	callCount := 0
	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
			if len(args) > 0 && args[0] == "log" {
				return "abc1234 commit", nil
			}
			return "", fmt.Errorf("not found")
		},
		gitRunFunc: func(args ...string) error {
			callCount++
			if callCount == 3 {	// tag -f master HEAD
				return fmt.Errorf("tag update failed")
			}
			return nil
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to update master tag")
}

func TestRealExecutorGitOutput(t *testing.T) {
	ex := realExecutor{}
	// We're in a git repo, so this should work
	out, err := ex.gitOutput("rev-parse", "--is-inside-work-tree")
	assert.Nil(t, err)
	assert.Equal(t, "true", out)
}

func TestRealExecutorGitOutputError(t *testing.T) {
	ex := realExecutor{}
	_, err := ex.gitOutput("rev-parse", "--verify", "nonexistent-ref-that-does-not-exist-xyz")
	assert.NotNil(t, err)
}

func TestRealExecutorGitRun(t *testing.T) {
	ex := realExecutor{}
	// git status always succeeds in a git repo
	err := ex.gitRun("status", "--porcelain")
	assert.Nil(t, err)
}

func TestRealExecutorGhReleaseError(t *testing.T) {
	ex := realExecutor{}
	// gh with invalid args should fail
	err := ex.ghRelease("create", "--invalid-flag-xyz")
	assert.NotNil(t, err)
}
