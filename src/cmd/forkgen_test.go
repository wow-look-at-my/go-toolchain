package cmd

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The go command this binary carries picks the C compiler of a cosmo target
// from this table. Without the cosmo rows it reaches for the host's gcc.
func TestDefaultCCBodyNamesTheCosmoccDrivers(t *testing.T) {
	body := defaultCCBody("cfg", "DefaultPkgConfig", "DefaultCC", "DefaultCXX")
	_, err := parser.ParseFile(token.NewFileSet(), "zdefaultcc.go", body, 0)
	require.NoError(t, err)
	for _, want := range []string{
		"case \"cosmo/amd64\":\n\t\treturn \"x86_64-unknown-cosmo-cc\"",
		"case \"cosmo/arm64\":\n\t\treturn \"aarch64-unknown-cosmo-cc\"",
		"case \"cosmo/amd64\":\n\t\treturn \"x86_64-unknown-cosmo-c++\"",
		"case \"cosmo/arm64\":\n\t\treturn \"aarch64-unknown-cosmo-c++\"",
		"return \"gcc\"",
		"return \"g++\"",
	} {
		assert.True(t, strings.Contains(body, want), "zdefaultcc.go lacks %q:\n%s", want, body)
	}
}
