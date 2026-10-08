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
	require.NoError(t, os.WriteFile(path, []byte(content), 0o755))
}

// newRatchetClone builds an origin whose master holds files, and returns a
// clone of it on a feature branch.
func newRatchetClone(t *testing.T, files map[string]string) string {
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
	// A run of this repository's own tests is the child of a parent that runs the ratchet, so the child's marker is cleared.
	t.Setenv(ratchetByParentEnv, "")
}

// judged is a repository whose master holds a guarantee: code.go says strong,
// and master's judge.sh fails a branch whose code.go does not.
var judged = map[string]string{
	ratchetFile: "# master judges every branch\nsh judge.sh\n",
	"judge.sh":  "grep -q strong \"$1/code.go\" || { echo \"weakened: $1/code.go\"; exit 1; }\n",
	"code.go":   "package x // strong\n",
}

// A branch that keeps the guarantee passes, and may change anything else.
func TestABranchThatKeepsTheGuaranteePasses(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newRatchetClone(t, judged)
	commitIn(t, clone, map[string]string{"code.go": "package x // strong, and better\n", "other.go": "package x\n"})

	assert.NoError(t, checkRatchetIn(clone))
}

// A branch that weakens what master judges fails.
func TestABranchThatWeakensTheGuaranteeFails(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newRatchetClone(t, judged)
	commitIn(t, clone, map[string]string{"code.go": "package x // weak\n"})

	err := checkRatchetIn(clone)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "master's .github/ratchet (sh judge.sh) fails on this branch")
}

// The judge is master's copy. A branch that rewrites it to pass, and then
// weakens the code, still fails.
func TestABranchCannotRewriteItsJudge(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newRatchetClone(t, judged)
	commitIn(t, clone, map[string]string{"judge.sh": "exit 0\n", "code.go": "package x // weak\n"})

	assert.Error(t, checkRatchetIn(clone))
}

// The command is master's too. A branch that points the file at a command that
// always passes still fails.
func TestABranchCannotRenameItsJudge(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newRatchetClone(t, judged)
	commitIn(t, clone, map[string]string{ratchetFile: "true\n", "code.go": "package x // weak\n"})

	assert.Error(t, checkRatchetIn(clone))
}

// A branch may make its own judge stricter. It takes effect once it lands on
// master, and until then master's judge decides.
func TestABranchMayStrengthenItsJudge(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newRatchetClone(t, judged)
	commitIn(t, clone, map[string]string{"judge.sh": "grep -q strong \"$1/code.go\" && grep -q tested \"$1/code.go\"\n"})

	assert.NoError(t, checkRatchetIn(clone))
}

// The judge runs from a checkout of master, which the run removes afterwards.
func TestTheJudgeRunsFromMastersCheckout(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	files := map[string]string{
		ratchetFile:      "sh judge.sh\n",
		"judge.sh":       "test -f only-on-master && test \"$(pwd)\" != \"$1\"\n",
		"only-on-master": "",
	}
	clone := newRatchetClone(t, files)
	gitIn(t, clone, "rm", "-q", "only-on-master")
	gitIn(t, clone, "commit", "-qm", "drop")

	require.NoError(t, checkRatchetIn(clone))
	worktrees, err := os.ReadDir(filepath.Join(clone, ".git", "worktrees"))
	if err == nil {
		assert.Empty(t, worktrees, "the run leaves no worktree behind")
	}
}

// The default branch is where an owner's merge lands a change to the judge, so
// its own run passes.
func TestTheDefaultBranchItselfPasses(t *testing.T) {
	t.Serial()
	inCIOn(t, "master")
	clone := newRatchetClone(t, judged)
	commitIn(t, clone, map[string]string{"code.go": "package x // weak\n"})

	assert.NoError(t, checkRatchetIn(clone))
}

// A repository whose default branch names no ratchet has nothing to hold a
// branch to. A file the branch adds counts once it lands on master.
func TestNoRatchetJudgesNothing(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newRatchetClone(t, map[string]string{"code.go": "package x // strong\n"})
	commitIn(t, clone, map[string]string{ratchetFile: "false\n"})

	assert.NoError(t, checkRatchetIn(clone))
}

// A ratchet file with no command is an error, never a pass.
func TestAnEmptyRatchetFails(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newRatchetClone(t, map[string]string{ratchetFile: "# nothing\n\n"})

	err := checkRatchetIn(clone)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "names no command")
}

// Outside CI the check never reaches the network.
func TestRatchetSkipsOutsideCI(t *testing.T) {
	t.Serial()
	t.Setenv("CI", "")
	assert.NoError(t, checkRatchetIn(t.TempDir()))
	assert.NoError(t, checkRatchet())
}

// A child run under the build it judges leaves the ratchet to its parent.
func TestAChildLeavesTheRatchetToItsParent(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	clone := newRatchetClone(t, judged)
	commitIn(t, clone, map[string]string{"code.go": "package x // weak\n"})

	t.Setenv(ratchetByParentEnv, "1")
	assert.NoError(t, checkRatchetIn(clone))
	t.Setenv(ratchetByParentEnv, "")
	assert.Error(t, checkRatchetIn(clone))
}

// A directory that is no repository has nothing to compare, so the run goes
// on to say what it does lack.
func TestADirectoryOutsideARepositoryHasNoRatchet(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	assert.NoError(t, checkRatchetIn(t.TempDir()))
}

// A CI run that cannot learn the default branch fails rather than passing unchecked.
func TestAnUnreachableOriginFails(t *testing.T) {
	t.Serial()
	inCIOn(t, "feature")
	dir, _ := newGoModRepo(t, "go 1.27")

	err := checkRatchetIn(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading the default branch")
}

// The default branch's checkout is generated in place, and the process comes
// back to the directory it was in.
func TestGenerateInKeepsTheWorkingDirectory(t *testing.T) {
	t.Serial()
	before, err := os.Getwd()
	require.NoError(t, err)

	module, _ := newGoModRepo(t, "go 1.27")
	require.NoError(t, generateIn(module))
	require.NoError(t, generateIn(t.TempDir()), "a directory with no go.mod generates nothing")
	assert.ErrorContains(t, generateIn(filepath.Join(t.TempDir(), "missing")), "missing")

	after, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

// Both pipelines run the ratchet.
func TestBothPipelinesRunTheRatchet(t *testing.T) {
	t.Serial()
	for _, file := range []string{"root.go", "matrix.go"} {
		source, err := os.ReadFile(file)
		require.NoError(t, err)
		assert.Contains(t, string(source), "if err := checkRatchet(); err != nil {", file)
	}
}

func TestRatchetCommandSkipsCommentsAndBlanks(t *testing.T) {
	t.Serial()
	assert.Equal(t, []string{"go", "run", "./cmd/ratchet"}, ratchetCommand("# note\n\n  go run ./cmd/ratchet  \nignored\n"))
	assert.Empty(t, ratchetCommand("# only a comment\n\n"))
}
