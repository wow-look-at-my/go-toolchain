package cmd

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/wow-look-at-my/go-containers/set"
)

func TestUsableRevisionRejectsWhatAnLDFlagsValueCannotCarry(t *testing.T) {
	assert.Equal(t, "deadbeef", usableRevision("  deadbeef\n"))
	assert.Equal(t, "", usableRevision(""))
	// The go command re-splits the -ldflags value, so a revision holding a
	// separator would silently become extra flags.
	assert.Equal(t, "", usableRevision("dead beef"))
	assert.Equal(t, "", usableRevision(`dead"beef`))
	assert.Equal(t, "", usableRevision("dead'beef"))
}

func TestRevisionStampNamesOnlyTheDeclaredVariables(t *testing.T) {
	declared := set.Of("gitHash", "Commit", "unrelated")
	got := revisionStamp("example.com/m/cmd/srv", declared, "abc123")
	assert.Equal(t, "-X example.com/m/cmd/srv.gitHash=abc123 -X example.com/m/cmd/srv.Commit=abc123", got,
		"a name the package does not declare must never reach the linker")
}

func TestRevisionStampIsEmptyForAPackageThatDeclaresNoStampVariable(t *testing.T) {
	assert.Equal(t, "", revisionStamp("example.com/m", set.Of("version", "buildDate"), "abc123"))
	assert.Equal(t, "", revisionStamp("example.com/m", set.New[string](), "abc123"))
}

func TestRevisionStampIsEmptyWhenTheBuildKnowsNoRevision(t *testing.T) {
	// Warning rather than silence is the point: the binary otherwise ships
	// its placeholder and reads as a development build wherever it lands.
	assert.Equal(t, "", revisionStamp("example.com/m", set.Of("gitHash"), ""))
}

func TestPackageDirMapsAnImportPathBackOntoTheTree(t *testing.T) {
	mod := "example.com/m"
	assert.Equal(t, ".", packageDir(mod, mod))
	assert.Equal(t, ".", packageDir("", "example.com/other/cmd/x"))
	assert.Equal(t, filepath.Join("cmd", "srv"), packageDir(mod, mod+"/cmd/srv"))
	assert.Equal(t, "", packageDir(mod, "example.com/other/cmd/x"),
		"a path outside the module has no source here to read")
}
