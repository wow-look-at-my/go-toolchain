package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// tidyMock returns a mock runner whose `go mod tidy` behavior is driven by
// fail decides per-call: writing marker stderr and failing while it returns
// true, succeeding when it returns false. calls counts tidy invocations.
func tidyMock(stderrLine string, failFirst int) (*runner.Mock, *int) {
	mock := runner.NewMock()
	calls := new(int)
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		if !cfg.IsCmd("go", "mod", "tidy") {
			return nil, nil
		}
		*calls++
		if *calls <= failFirst {
			if cfg.StderrWriter != nil {
				_, _ = cfg.StderrWriter.Write([]byte(stderrLine))
			}
			return runner.MockProcess(nil, errors.New("exit status 1")), nil
		}
		return runner.MockProcess(nil, nil), nil
	}
	return mock, calls
}

// chdirWithGoMod moves the test into a temp module dir so runModTidy's
// go.mod existence probe sees a module.
func chdirWithGoMod(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module tidytest\n\ngo 1.24\n"), 0o644))
	t.Chdir(dir)
}

func TestRunModTidyCorruptIndexRetriesWithIndexDisabled(t *testing.T) {
	t.Setenv("GODEBUG", "")
	chdirWithGoMod(t)

	mock, calls := tidyMock("go: golang.org/x/exp/slices: corrupt index\n", 1)
	require.NoError(t, runModTidy(mock, true))
	require.Equal(t, 2, *calls)
	require.Contains(t, os.Getenv("GODEBUG"), "goindex=0")
}

func TestRunModTidyCorruptIndexRetryStillFailing(t *testing.T) {
	t.Setenv("GODEBUG", "")
	chdirWithGoMod(t)

	mock, calls := tidyMock("go: golang.org/x/exp/slices: corrupt index\n", 2)
	err := runModTidy(mock, true)
	require.ErrorContains(t, err, "go mod tidy failed")
	require.Equal(t, 2, *calls, "must retry exactly once")
}

func TestRunModTidyOtherFailureDoesNotRetry(t *testing.T) {
	t.Setenv("GODEBUG", "")
	chdirWithGoMod(t)

	mock, calls := tidyMock("go: some unrelated resolution error\n", 1)
	err := runModTidy(mock, true)
	require.ErrorContains(t, err, "go mod tidy failed")
	require.Equal(t, 1, *calls)
	require.NotContains(t, os.Getenv("GODEBUG"), "goindex=0")
}

func TestRunModTidySuccessTouchesNothing(t *testing.T) {
	t.Setenv("GODEBUG", "")
	chdirWithGoMod(t)

	mock, calls := tidyMock("", 0)
	require.NoError(t, runModTidy(mock, true))
	require.Equal(t, 1, *calls)
	require.NotContains(t, os.Getenv("GODEBUG"), "goindex=0")
}

func TestRunModTidyMissingGoModMessage(t *testing.T) {
	t.Setenv("GODEBUG", "")
	t.Chdir(t.TempDir()) // no go.mod here

	mock, _ := tidyMock("go: no module\n", 1)
	err := runModTidy(mock, true)
	require.ErrorContains(t, err, "no go.mod found")
}

func TestDisableGoModuleIndexMergesExistingGODEBUG(t *testing.T) {
	t.Setenv("GODEBUG", "http2client=0")
	disableGoModuleIndex()
	require.Equal(t, "http2client=0,goindex=0", os.Getenv("GODEBUG"))

	t.Setenv("GODEBUG", "")
	disableGoModuleIndex()
	require.Equal(t, "goindex=0", os.Getenv("GODEBUG"))
}

func TestTailBufferKeepsBoundedTail(t *testing.T) {
	t.Parallel()
	var tb tailBuffer
	chunk := strings.Repeat("x", 40<<10)
	_, err := tb.Write([]byte(chunk))
	require.NoError(t, err)
	_, err = tb.Write([]byte(chunk))
	require.NoError(t, err)
	_, err = tb.Write([]byte("tail-marker"))
	require.NoError(t, err)
	require.LessOrEqual(t, len(tb.String()), tailBufferCap)
	require.True(t, strings.HasSuffix(tb.String(), "tail-marker"))
}
