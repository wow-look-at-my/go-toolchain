package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newGoModRepo builds a repository whose only committed file is go.mod, and
// returns the repository and that file.
func newGoModRepo(t *testing.T, goLine string) (dir, mod string) {
	t.Helper()
	dir = t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
	} {
		require.NoError(t, exec.Command("git", append([]string{"-C", dir}, args...)...).Run())
	}
	mod = filepath.Join(dir, "go.mod")
	require.NoError(t, os.WriteFile(mod, []byte("module example.com/x\n\n"+goLine+"\n"), 0644))
	require.NoError(t, exec.Command("git", "-C", dir, "add", "go.mod").Run())
	require.NoError(t, exec.Command("git", "-C", dir, "commit", "-qm", "init").Run())
	return dir, mod
}

// A rewrite that lands the same bytes is not an edit. The windows leg failed a
// green build over such a rewrite, so the refresh must clear it - and must
// still name a file whose content really moved.
func TestRefreshGitIndexClearsAStatOnlyChange(t *testing.T) {
	t.Serial()
	dir, mod := newGoModRepo(t, "go 1.27")

	require.NoError(t, os.WriteFile(mod, []byte("module example.com/x\n\ngo 1.27\n"), 0644))
	assert.Empty(t, refreshGitIndex(dir), "identical bytes are not a change")

	require.NoError(t, os.WriteFile(mod, []byte("module example.com/x\n\ngo 1.28\n"), 0644))
	assert.Contains(t, refreshGitIndex(dir), "go.mod", "a real edit still needs updating")
}

// An empty diff reads either way, and the reader is told which.
func TestNoContentChangeReportNamesTheDisagreement(t *testing.T) {
	t.Serial()
	dir, mod := newGoModRepo(t, "go 1.27")
	assert.Contains(t, noContentChangeReport(dir), "untracked or already committed")

	require.NoError(t, os.WriteFile(mod, []byte("module example.com/x\n\ngo 1.28\n"), 0644))
	assert.Contains(t, noContentChangeReport(dir), "disagree")
}

func TestCheckDirtyInCISkipsOutsideCI(t *testing.T) {
	t.Serial()
	t.Setenv("CI", "")
	assert.NoError(t, checkDirtyInCI())
}

// The message tells the reader to review the diff, so a CI-only failure has to
// carry it: the runner's tree is gone by the time anyone reads the log.
func TestDirtyDiffPaths(t *testing.T) {
	t.Serial()
	status := " M go.mod\n?? build/extra.txt\nR  old.go -> new.go\n"
	assert.Equal(t, []string{"go.mod", "build/extra.txt", "new.go"}, dirtyDiffPaths(status))
	assert.Empty(t, dirtyDiffPaths(""))
}

// A dirty tree in CI is often the only place a change is visible, so the diff
// has to arrive or say why it did not. Returning nothing leaves the reader
// staring at a file list under an instruction to review something absent.
func TestDirtyDiffShowsTheChange(t *testing.T) {
	t.Serial()
	dir, mod := newGoModRepo(t, "go 1.27")
	require.NoError(t, os.WriteFile(mod, []byte("module example.com/x\n\ngo 1.28\n"), 0644))

	got := dirtyDiffIn(dir, " M go.mod")
	assert.Contains(t, got, "-go 1.27")
	assert.Contains(t, got, "+go 1.28")

	// Staged is not the same as absent, and the reader is told which.
	require.NoError(t, exec.Command("git", "-C", dir, "add", "go.mod").Run())
	assert.Contains(t, dirtyDiffIn(dir, " M go.mod"), "(staged)")
}

// Every path out of dirtyDiff says something. Silence is what sent the last
// windows failure back around with nothing learned.
func TestDirtyDiffReportsWhenGitCannotAnswer(t *testing.T) {
	t.Serial()
	assert.Contains(t, dirtyDiffIn(t.TempDir(), " M go.mod"), "git diff failed")
	assert.Empty(t, dirtyDiffIn(t.TempDir(), ""))
}
