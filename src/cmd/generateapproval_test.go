package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/modfile"
)

const approvalGoMod = `module example.com/m // go-toolchain:generate=own123

go 1.27

require (
	github.com/wow/plain v1.0.0 // go-toolchain:generate=abc123
	github.com/wow/deep v0.0.0-20260913013131-12eca33b8f79 // indirect; go-toolchain:generate=def456
	github.com/wow/bare v1.0.0
	github.com/up/fork v1.0.0
)

replace github.com/up/fork => github.com/wow/fork v1.1.0 // go-toolchain:generate=f0f0f0
`

// withGoMod puts a go.mod in a fresh directory and answers the directory.
func withGoMod(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(body), 0o644))
	return dir
}

// parsedGoMod parses the fixture go.mod.
func parsedGoMod(t *testing.T) *modfile.File {
	t.Helper()
	f, err := readGoModFile(withGoMod(t, approvalGoMod))
	require.NoError(t, err)
	return f
}

// withFlag sets --generate for the length of the test.
func withFlag(t *testing.T, value string) {
	t.Helper()
	prev := generateHash
	generateHash = value
	t.Cleanup(func() { generateHash = prev })
}

// The module line records the tree's own approval, so a fresh clone needs no
// argument to build a repo whose output a directive writes.
func TestTheModuleLineRecordsTheTreesOwnApproval(t *testing.T) {
	t.Serial()
	withFlag(t, "")
	t.Chdir(withGoMod(t, approvalGoMod))

	assert.Equal(t, "own123", approvedGenerateHash())
}

// A tree that records nothing approves nothing, which is where every repo starts.
func TestAModuleLineWithoutAMarkerApprovesNothing(t *testing.T) {
	t.Serial()
	withFlag(t, "")
	t.Chdir(withGoMod(t, "module example.com/m\n\ngo 1.27\n"))

	assert.Empty(t, approvedGenerateHash())
}

func TestTheFlagWinsOverTheModuleLine(t *testing.T) {
	t.Serial()
	withFlag(t, "fromflag")
	t.Chdir(withGoMod(t, approvalGoMod))

	assert.Equal(t, "fromflag", approvedGenerateHash())
}

// A dependency's approval rides its require line, beside the markers already there.
func TestADependencyApprovalIsReadOffItsRequireLine(t *testing.T) {
	f := parsedGoMod(t)
	assert.Equal(t, "abc123", dependencyApproval(f, "github.com/wow/plain"))
	assert.Equal(t, "def456", dependencyApproval(f, "github.com/wow/deep"), "an indirect comment leads the line")
	assert.Empty(t, dependencyApproval(f, "github.com/wow/bare"))
	assert.Empty(t, dependencyApproval(f, "github.com/wow/absent"))
	assert.Empty(t, dependencyApproval(nil, "github.com/wow/plain"))
}

// A fork is cached under its replacement's path, so the replace line speaks for it.
func TestAForkIsApprovedOnItsReplaceLine(t *testing.T) {
	assert.Equal(t, "f0f0f0", dependencyApproval(parsedGoMod(t), "github.com/wow/fork"))
}

// The line to write keeps every comment the line already carries, and leaves
// the parsed file as it was.
func TestTheApprovedLineKeepsTheExistingComment(t *testing.T) {
	f := parsedGoMod(t)
	assert.Equal(t,
		"github.com/wow/bare v1.0.0 // go-toolchain:generate=new789",
		approvedLine(f, "github.com/wow/bare", "v1.0.0", "new789"))
	assert.Equal(t,
		"github.com/wow/deep v0.0.0-20260913013131-12eca33b8f79 // indirect; go-toolchain:generate=new789",
		approvedLine(f, "github.com/wow/deep", "v0.0.0-20260913013131-12eca33b8f79", "new789"),
		"a stale hash is replaced, not repeated")
	assert.Equal(t, "def456", dependencyApproval(f, "github.com/wow/deep"), "the file itself is untouched")
}

// A module go.mod does not name gets a whole require line to add.
func TestAnUnlistedModuleGetsARequireLine(t *testing.T) {
	assert.Equal(t,
		"require github.com/wow/absent v2.0.0 // go-toolchain:generate=new789",
		approvedLine(parsedGoMod(t), "github.com/wow/absent", "v2.0.0", "new789"))
}

// The tree's own approval is written on its module line.
func TestTheApprovedModuleLineReplacesTheOldHash(t *testing.T) {
	assert.Equal(t,
		"module example.com/m // go-toolchain:generate=new789",
		approvedModuleLine(withGoMod(t, approvalGoMod), "new789"))
	assert.Equal(t,
		"// go-toolchain:generate=new789",
		approvedModuleLine(t.TempDir(), "new789"), "with no go.mod there is only the marker to name")
}

// The approval is read off a line that carries another comment beside it.
func TestTheApprovalReadsBesideAnotherComment(t *testing.T) {
	named := &modfile.Line{Comments: modfile.Comments{Suffix: []modfile.Comment{{Token: "// indirect; go-toolchain:generate=abc123"}}}}
	assert.Equal(t, "abc123", parseGenerateMarker(named))
}
