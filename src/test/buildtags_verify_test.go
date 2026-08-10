package gotest

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/buildtags"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// A listing that returns nothing means no go command ran, not that every
// gated file is unreachable. Failing there is a guard firing when it could not
// look -- and it fired on every caller driving the phase without a toolchain.
func TestVerifyTagCoverageDoesNotFailOnAnEmptyListing(t *testing.T) {
	d := &buildtags.Discovery{
		Configs: []buildtags.Config{{}, {Tags: []string{"cosmo"}}},
		Gated:   []buildtags.File{{Path: "xattr_cosmo.go", Tags: []string{"cosmo"}}},
	}
	mock := runner.NewMock()
	mock.Handler = func(runner.Config) (runner.IProcess, error) {
		return runner.MockProcess(nil, nil), nil
	}
	require.NoError(t, verifyTagCoverage(mock, d))
}

// The check still fails when the listing DID answer and the file is genuinely
// out of reach -- otherwise the leniency above would have removed the guard.
func TestVerifyTagCoverageStillFailsWhenTheListingAnswersWithoutTheFile(t *testing.T) {
	d := &buildtags.Discovery{
		Configs: []buildtags.Config{{}},
		Gated:   []buildtags.File{{Path: "xattr_cosmo.go", Tags: []string{"cosmo"}}},
	}
	root, err := os.Getwd()
	require.NoError(t, err)
	mock := runner.NewMock()
	mock.Handler = func(runner.Config) (runner.IProcess, error) {
		return runner.MockProcess([]byte(root+"/other.go\n"), nil), nil
	}
	err = verifyTagCoverage(mock, d)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "xattr_cosmo.go")
}
