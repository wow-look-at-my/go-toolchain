package profile

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
)

func TestCollector_GraphArgUniqueAndRecorded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "profile")
	c := NewCollector(dir)

	a1 := c.GraphArg()
	a2 := c.GraphArg()
	require.True(t, strings.HasPrefix(a1, "-debug-actiongraph="))
	require.True(t, strings.HasPrefix(a2, "-debug-actiongraph="))
	assert.NotEqual(t, a1, a2, "each invocation gets its own dump file")

	files := c.Files()
	require.Len(t, files, 2)
	assert.Equal(t, a1, "-debug-actiongraph="+files[0])
	assert.Equal(t, a2, "-debug-actiongraph="+files[1])

	// The dump dir must exist so the go command can create the file.
	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestCollector_RemovesStaleDump(t *testing.T) {
	dir := t.TempDir()
	c := NewCollector(dir)
	arg := c.GraphArg()
	path := strings.TrimPrefix(arg, "-debug-actiongraph=")
	require.NoError(t, os.WriteFile(path, []byte("stale"), 0o644))

	// A new collector for the same pid reissues the same path and clears stale content.
	c2 := NewCollector(dir)
	arg2 := c2.GraphArg()
	require.Equal(t, arg, arg2)
	_, err := os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestCollector_GraphArgConcurrent(t *testing.T) {
	c := NewCollector(t.TempDir())
	var wg sync.WaitGroup
	args := make([]string, 16)
	for i := range args {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args[i] = c.GraphArg()
		}(i)
	}
	wg.Wait()
	seen := set.New[string]()
	for _, a := range args {
		require.NotEmpty(t, a)
		assert.True(t, seen.Add(a), "concurrent GraphArg calls must not collide")
	}
	assert.Len(t, c.Files(), 16)
}

func TestCollector_UncreatableDirDisables(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "afile")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	c := NewCollector(filepath.Join(f, "sub")) // parent is a file
	assert.Equal(t, "", c.GraphArg())
	assert.Empty(t, c.Files())
}

func TestPackageLevelGraphArg(t *testing.T) {
	SetActive(nil)
	assert.Equal(t, "", GraphArg(), "no active collector: no injection")

	c := NewCollector(t.TempDir())
	SetActive(c)
	t.Cleanup(func() { SetActive(nil) })
	arg := GraphArg()
	assert.True(t, strings.HasPrefix(arg, "-debug-actiongraph="))
	assert.Len(t, c.Files(), 1)
}
