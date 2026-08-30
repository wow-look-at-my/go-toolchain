package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Asserted against the source: the file is GOOS=cosmo and test binaries build for the host, so nothing here runs it.
const fifoPeerFile = "claudeguard_fifopeer_cosmo.go"

// Every lsof invocation goes through lsofCommand, which is the only place the
// deadline and WaitDelay are set. A bare exec.Command here is the defect.
func TestFifoPeerLsofAlwaysBounded(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, fifoPeerFile, nil, 0)
	require.NoError(t, err, "parse %s", fifoPeerFile)

	var bare []string
	var bounded int
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "lsofCommand" {
				bounded++
			}
		case *ast.SelectorExpr:
			pkg, ok := fn.X.(*ast.Ident)
			if !ok || pkg.Name != "exec" {
				return true
			}
			if fn.Sel.Name == "Command" {
				bare = append(bare, fset.Position(call.Pos()).String())
			}
		}
		return true
	})

	assert.Empty(t, bare, "%s must reach lsof only through lsofCommand, which bounds it; "+
		"a bare exec.Command has no deadline and hangs the guard before main", fifoPeerFile)
	assert.NotZero(t, bounded, "%s must call lsofCommand; if it stopped, this test is asserting nothing", fifoPeerFile)
}

// WaitDelay is the half that is easy to leave off and useless to omit: a
// context kills the child, and Wait still blocks until every holder of its
// output pipe closes it. lsof forks helpers that inherit that pipe.
func TestFifoPeerSetsWaitDelay(t *testing.T) {
	src, err := os.ReadFile(fifoPeerFile)
	require.NoError(t, err)
	assert.Contains(t, string(src), "WaitDelay",
		"%s must set WaitDelay on the lsof command: cancelling the context alone leaves Wait blocked", fifoPeerFile)
	assert.Contains(t, string(src), "exec.CommandContext",
		"%s must build the lsof command with a context", fifoPeerFile)
}

// The ancestor walk calls into a dependency whose own bound is per-call, so a
// long chain multiplies it. The loop has to consult the deadline itself.
func TestFifoPeerWalkChecksTheDeadline(t *testing.T) {
	src, err := os.ReadFile(fifoPeerFile)
	require.NoError(t, err)
	body := string(src)
	require.Contains(t, body, "for hops := 0;", "the ancestor walk moved; this test no longer covers it")
	walk := body[strings.Index(body, "for hops := 0;"):]
	end := strings.Index(walk, "\n\t}")
	require.Positive(t, end, "could not find the end of the ancestor walk")
	assert.Contains(t, walk[:end], "ctx.Err()",
		"the ancestor walk must check the deadline between hops: a per-call timeout bounds a single ps, not a chain of them")
}
