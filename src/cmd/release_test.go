package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

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
	commits := parseCommitLines("abc1234")
	assert.Equal(t, []string{"abc1234"}, commits)
}

// mockExecutor implements releaseExecutor for testing.
type mockExecutor struct {
	gitOutputCalls [][]string
	gitRunCalls    [][]string

	gitOutputFunc func(args ...string) (string, error)
	gitRunFunc    func(args ...string) error
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

	oldTag := releaseTag
	oldFrom := releaseFrom
	releaseTag = "v1.0.0"
	releaseFrom = "v0.9.0"
	defer func() {
		releaseTag = oldTag
		releaseFrom = oldFrom
	}()

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
	assert.Equal(t, 4, len(mock.gitRunCalls))
	assert.Equal(t, []string{"tag", "v1.0.0"}, mock.gitRunCalls[0])
	assert.Equal(t, []string{"push", "origin", "v1.0.0"}, mock.gitRunCalls[1])
	assert.Equal(t, []string{"tag", "-f", "latest", "HEAD"}, mock.gitRunCalls[2])
	assert.Equal(t, []string{"push", "-f", "origin", "refs/tags/latest"}, mock.gitRunCalls[3])
}

func TestReleaseCmdAutoTag(t *testing.T) {
	t.Setenv("CI", "true")

	oldTag := releaseTag
	oldFrom := releaseFrom
	releaseTag = "" // auto-detect
	releaseFrom = ""
	defer func() {
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
			if callCount == 2 { // push origin <tag>
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
	releaseTag = "" // auto-detect
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
			if callCount == 3 { // tag -f latest HEAD
				return fmt.Errorf("tag update failed")
			}
			return nil
		},
	}

	err := runReleaseCmdImpl(strings.NewReader(""), mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to update latest tag")
}

func TestRealExecutorGitOutput(t *testing.T) {
	ex := realExecutor{}
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
	err := ex.gitRun("status", "--porcelain")
	assert.Nil(t, err)
}

func TestCollectCommitsWithExecutor(t *testing.T) {
	mock := &mockExecutor{
		gitOutputFunc: func(args ...string) (string, error) {
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
