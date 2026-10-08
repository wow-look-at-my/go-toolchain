package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeIn writes content to name under dir, making its directory.
func writeIn(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// newProtectedClone builds an origin whose master holds files, and returns a
// clone of it on a feature branch.
func newProtectedClone(t *testing.T, files map[string]string) string {
	t.Helper()
	origin := t.TempDir()
	gitIn(t, origin, "init", "-q", "--bare", "-b", "master")

	seed := t.TempDir()
	gitIn(t, seed, "init", "-q", "-b", "master")
	gitIn(t, seed, "config", "user.email", "t@example.com")
	gitIn(t, seed, "config", "user.name", "t")
	for name, content := range files {
		writeIn(t, seed, name, content)
	}
	gitIn(t, seed, "add", "-A")
	gitIn(t, seed, "commit", "-qm", "init")
	gitIn(t, seed, "push", "-q", "file://"+origin, "master")

	clone := t.TempDir()
	gitIn(t, clone, "clone", "-q", "file://"+origin, ".")
	gitIn(t, clone, "config", "user.email", "t@example.com")
	gitIn(t, clone, "config", "user.name", "t")
	gitIn(t, clone, "checkout", "-q", "-b", "feature")
	return clone
}

// commitIn writes files in dir and commits them.
func commitIn(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		writeIn(t, dir, name, content)
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-qm", "change")
}

// inCIOn sets the environment of a CI run for branch.
func inCIOn(t *testing.T, branch string) {
	t.Helper()
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_REF_NAME", branch)
}

var guarded = map[string]string{
	protectedPathsFile:  "# the guarantee\nguarantee_test.go\n\ntestdata/corpus\n" + protectedPathsFile + "\n",
	"guarantee_test.go": "package x\n",
	"testdata/corpus/a": "one\n",
	"code.go":           "package x\n",
}

// A branch that edits a protected file fails, and the message names the file
// and how to put it back.
func TestABranchMayNotChangeAProtectedFile(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newProtectedClone(t, guarded)
	commitIn(t, clone, map[string]string{"guarantee_test.go": "package x\n\nfunc weaker() {}\n"})

	err := checkProtectedPathsIn(clone)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "guarantee_test.go")
	assert.Contains(t, err.Error(), "git checkout origin/master --")
}

// A directory on the list protects every file under it, an added one included.
func TestAProtectedDirectoryCoversWhatIsAddedToIt(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newProtectedClone(t, guarded)
	commitIn(t, clone, map[string]string{"testdata/corpus/b": "two\n"})

	err := checkProtectedPathsIn(clone)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "testdata/corpus/b")
}

// Deleting a protected file is a change to it.
func TestDeletingAProtectedFileFails(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newProtectedClone(t, guarded)
	gitIn(t, clone, "rm", "-q", "guarantee_test.go")
	gitIn(t, clone, "commit", "-qm", "drop")

	assert.Error(t, checkProtectedPathsIn(clone))
}

// The list is read from the default branch, so a branch that takes a path off
// the list and then edits it still fails. That list is on both files.
func TestABranchCannotShortenTheList(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newProtectedClone(t, guarded)
	commitIn(t, clone, map[string]string{
		protectedPathsFile:  protectedPathsFile + "\n",
		"guarantee_test.go": "package x\n\nfunc weaker() {}\n",
	})

	err := checkProtectedPathsIn(clone)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "guarantee_test.go")
	assert.Contains(t, err.Error(), protectedPathsFile)
}

// Code outside the list changes freely.
func TestAnUnprotectedChangePasses(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newProtectedClone(t, guarded)
	commitIn(t, clone, map[string]string{"code.go": "package x\n\nfunc better() {}\n"})

	assert.NoError(t, checkProtectedPathsIn(clone))
}

// The default branch is where an owner's merge lands a change to the list, so
// its own run passes.
func TestTheDefaultBranchItselfPasses(t *testing.T) {
	t.Serial()
	inCIOn(t, "master")
	clone := newProtectedClone(t, guarded)
	commitIn(t, clone, map[string]string{"guarantee_test.go": "package x\n\nfunc stronger() {}\n"})

	assert.NoError(t, checkProtectedPathsIn(clone))
}

// A repository whose default branch lists nothing has nothing protected.
func TestNoListProtectsNothing(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newProtectedClone(t, map[string]string{"guarantee_test.go": "package x\n"})
	commitIn(t, clone, map[string]string{
		protectedPathsFile:  "guarantee_test.go\n",
		"guarantee_test.go": "package x\n\nfunc weaker() {}\n",
	})

	assert.NoError(t, checkProtectedPathsIn(clone), "a list the branch adds protects nothing until it lands on the default branch")
}

// Outside CI the check never reaches the network.
func TestProtectedPathsSkipOutsideCI(t *testing.T) {
	t.Serial()
	t.Setenv("CI", "")
	assert.NoError(t, checkProtectedPathsIn(t.TempDir()))
	assert.NoError(t, checkProtectedPaths())
}

// A CI run that cannot learn the default branch fails rather than passing unchecked.
func TestAnUnreachableOriginFails(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	dir, _ := newGoModRepo(t, "go 1.27")

	err := checkProtectedPathsIn(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading the default branch")
}

// Both pipelines run the gate. Removing either call leaves that pipeline
// unguarded, so this file, which the repository protects, pins them.
func TestBothPipelinesRunTheProtectedPathsGate(t *testing.T) {
	t.Serial()
	for _, file := range []string{"root.go", "matrix.go"} {
		source, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Contains(t, string(source), "if err := checkProtectedPaths(); err != nil {", file)
	}
}

func TestParseProtectedPathsSkipsCommentsAndBlanks(t *testing.T) {
	t.Serial()
	assert.Equal(t, []string{"a_test.go", "testdata/x"}, parseProtectedPaths("# note\n\n  a_test.go  \ntestdata/x\n"))
	assert.Empty(t, parseProtectedPaths("# only a comment\n\n"))
}
