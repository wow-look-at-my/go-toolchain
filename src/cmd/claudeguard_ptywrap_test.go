package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeAncestry hands ptyWrapperAncestor a chain without a real process tree.
type fakeAncestry map[int]struct {
	comm string
	ppid int
}

func (c fakeAncestry) lookup(pid int) (string, int, bool) {
	e, ok := c[pid]
	return e.comm, e.ppid, ok
}

func withFakeAncestry(t *testing.T, start int, chain fakeAncestry) {
	t.Helper()
	t.Serial() // The seams are the package's, so replacing them replaces them for every test.
	origParent, origLookup := ptyWrapperParentPIDFn, ptyWrapperCommPPIDFn
	t.Cleanup(func() { ptyWrapperParentPIDFn, ptyWrapperCommPPIDFn = origParent, origLookup })
	ptyWrapperParentPIDFn = func() int { return start }
	ptyWrapperCommPPIDFn = chain.lookup
}

func TestPtyWrapperAncestor(t *testing.T) {
	t.Run("finds a known wrapper a few hops up", func(t *testing.T) {
		withFakeAncestry(t, 100, fakeAncestry{
			100: {"sh", 50},
			50:  {"script", 10},
			10:  {"bash", 2},
		})
		name, ok := ptyWrapperAncestor()
		assert.True(t, ok)
		assert.Equal(t, "script", name)
	})

	t.Run("every known wrapper name is matched", func(t *testing.T) {
		for _, name := range ptyWrapperProcs.Values() {
			withFakeAncestry(t, 100, fakeAncestry{100: {name, 1}})
			got, ok := ptyWrapperAncestor()
			assert.True(t, ok, "%s should be recognized", name)
			assert.Equal(t, name, got)
		}
	})

	t.Run("no wrapper anywhere in a fully resolvable chain answers not found", func(t *testing.T) {
		withFakeAncestry(t, 100, fakeAncestry{
			100: {"sh", 50},
			50:  {"bash", 10},
			10:  {"systemd", 1},
		})
		name, ok := ptyWrapperAncestor()
		assert.False(t, ok)
		assert.Empty(t, name)
	})

	t.Run("an unresolvable ancestor ends the walk without a guess", func(t *testing.T) {
		withFakeAncestry(t, 100, fakeAncestry{}) // the starting pid itself is not in the map
		name, ok := ptyWrapperAncestor()
		assert.False(t, ok)
		assert.Empty(t, name)
	})

	t.Run("a name only prefixed by a wrapper's name does not match", func(t *testing.T) {
		withFakeAncestry(t, 100, fakeAncestry{100: {"scripts-runner", 1}})
		_, ok := ptyWrapperAncestor()
		assert.False(t, ok)
	})

	t.Run("tmux and screen are not treated as recording wrappers", func(t *testing.T) {
		for _, name := range []string{"tmux", "screen"} {
			withFakeAncestry(t, 100, fakeAncestry{100: {name, 1}})
			_, ok := ptyWrapperAncestor()
			assert.False(t, ok, "%s relays to a real display, not a log file", name)
		}
	})

	t.Run("stops at maxHops rather than looping forever on a cycle", func(t *testing.T) {
		chain := fakeAncestry{} // never reaches the init pid, so only maxHops ends it
		pid := 100
		for i := 0; i < ptyWrapperMaxHops+10; i++ {
			next := pid + 1000
			chain[pid] = struct {
				comm string
				ppid int
			}{"sh", next}
			pid = next
		}
		withFakeAncestry(t, 100, chain)
		name, ok := ptyWrapperAncestor()
		assert.False(t, ok)
		assert.Empty(t, name)
	})
}
