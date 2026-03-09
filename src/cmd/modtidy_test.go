package cmd

import (
	"fmt"
	"os"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestIsDownloadError(t *testing.T) {
	tests := []struct {
		err    string
		expect bool
	}{
		{"dial tcp: lookup dl.google.com: no such host", true},
		{"dial tcp [::1]:53: connection refused", true},
		{"download go1.24.11: i/o timeout", true},
		{"TLS handshake timeout", true},
		{"exit status 1", false},
		{"mod tidy failed", false},
	}
	for _, tc := range tests {
		t.Run(tc.err, func(t *testing.T) {
			assert.Equal(t, tc.expect, isDownloadError(fmt.Errorf("%s", tc.err)))
		})
	}
}

func TestRunModTidySuccess(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\ngo 1.21\n"), 0644)

	mock := runner.NewMock()
	err := runModTidy(mock)
	assert.Nil(t, err)
}

func TestRunModTidyDownloadErrorRetries(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\ngo 1.21\n"), 0644)

	callCount := 0
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "mod") {
			callCount++
			if callCount == 1 {
				return runner.MockProcess(nil, fmt.Errorf("dial tcp: lookup dl.google.com: no such host")), nil
			}
			return nil, nil // success on retry
		}
		return nil, nil
	}

	err := runModTidy(mock)
	assert.Nil(t, err)
	assert.Equal(t, 2, callCount)
}

func TestRunModTidyNonDownloadErrorNoRetry(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	os.WriteFile("go.mod", []byte("module test\ngo 1.21\n"), 0644)

	callCount := 0
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "mod") {
			callCount++
			return runner.MockProcess(nil, fmt.Errorf("syntax error in go.mod")), nil
		}
		return nil, nil
	}

	err := runModTidy(mock)
	assert.NotNil(t, err)
	assert.Equal(t, 1, callCount)
}

func TestRunModTidyNoGoMod(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if cfg.IsCmd("go", "mod") {
			return runner.MockProcess(nil, fmt.Errorf("exit status 1")), nil
		}
		return nil, nil
	}

	err := runModTidy(mock)
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "no go.mod found")
}
