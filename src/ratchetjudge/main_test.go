package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const baseTests = "package cmd\n\nfunc TestKeepsA(t *testing.T) {}\n\nfunc TestKeepsB(t *testing.T) {}\n"

// checkouts builds a base checkout with the ratchet tests and a branch
// repository whose own copy of them is empty.
func checkouts(t *testing.T) (base, head string) {
	t.Helper()
	base = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, "src", "cmd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(base, testFile), []byte(baseTests), 0o644))

	head = t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(head, "src", "cmd"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(head, testFile), []byte("package cmd\n"), 0o644))
	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "init"},
	} {
		out, err := exec.Command("git", append([]string{"-C", head}, args...)...).CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	return base, head
}

// The base's tests run in the branch's tree, over the branch's own copy.
func TestEveryBaseTestPassingPasses(t *testing.T) {
	base, head := checkouts(t)
	err := judge(base, []string{head}, func(tree string, names []string) (string, error) {
		copied, err := os.ReadFile(filepath.Join(tree, testFile))
		require.NoError(t, err)
		assert.Equal(t, baseTests, string(copied))
		assert.Equal(t, []string{"TestKeepsA", "TestKeepsB"}, names)
		return "--- PASS: TestKeepsA (0.00s)\n--- PASS: TestKeepsB (0.00s)\n", nil
	})
	assert.NoError(t, err)
}

// A test that never reports a pass is a failure, even when go test exits 0, as
// it does when a branch's TestMain skips the run.
func TestATestThatDidNotPassFails(t *testing.T) {
	base, head := checkouts(t)
	err := judge(base, []string{head}, func(string, []string) (string, error) {
		return "--- PASS: TestKeepsA (0.00s)\nok\n", nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "TestKeepsB did not pass")
	assert.NotContains(t, err.Error(), "TestKeepsA,")
}

func TestAFailingRunFails(t *testing.T) {
	base, head := checkouts(t)
	err := judge(base, []string{head}, func(string, []string) (string, error) {
		return "--- PASS: TestKeepsA (0.00s)\n--- PASS: TestKeepsB (0.00s)\nFAIL\n", errors.New("exit status 1")
	})
	assert.Error(t, err)
}

func TestJudgeTakesOneCheckout(t *testing.T) {
	assert.ErrorContains(t, judge(".", nil, nil), "usage")
}

func TestABaseWithNoTestsFails(t *testing.T) {
	base, head := checkouts(t)
	require.NoError(t, os.WriteFile(filepath.Join(base, testFile), []byte("package cmd\n"), 0o644))
	assert.ErrorContains(t, judge(base, []string{head}, nil), "holds no test")
}

func TestABranchThatIsNoRepositoryFails(t *testing.T) {
	base, _ := checkouts(t)
	assert.ErrorContains(t, judge(base, []string{t.TempDir()}, nil), "checking out")
}

// A tree with no module is a failed run that says why.
func TestGoTestOutsideAModuleFails(t *testing.T) {
	out, err := goTest(t.TempDir(), []string{"TestX"})
	assert.Error(t, err)
	assert.NotEmpty(t, out)
}
