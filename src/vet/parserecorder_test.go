package vet

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
)

// x/tools calls ParseFile per goroutine per file, so this is the shape the
// real loader uses. Unlocked, it dies with "fatal error: concurrent map
// writes" -- a fatal error, not a panic, so no recover can hide it.
func TestParseRecorderSurvivesConcurrentRecording(t *testing.T) {
	t.Serial()
	root := t.TempDir()
	rec := &parseRecorder{files: set.New[string](), root: root}

	const goroutines, each = 32, 64
	var wg sync.WaitGroup
	for g := range goroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				rec.record(filepath.Join(root, fmt.Sprintf("pkg%d/file%d.go", g, i)))
			}
		}()
	}
	wg.Wait()

	assert.Equal(t, goroutines*each, rec.count())
	assert.Equal(t, goroutines*each, rec.files.Len())
	assert.True(t, rec.files.Contains("pkg0/file0.go"), "paths are recorded module-relative and slash separated")
}

// A file outside the module root belongs to no package of this module, so it
// must not land in the set Verify checks against.
func TestParseRecorderSkipsFilesOutsideTheModule(t *testing.T) {
	t.Serial()
	root := t.TempDir()
	rec := &parseRecorder{files: set.New[string](), root: filepath.Join(root, "mod")}

	rec.record(filepath.Join(root, "elsewhere", "x.go"))

	require.Equal(t, 1, rec.count(), "the counter reports files parsed, including foreign ones")
	assert.True(t, rec.files.IsEmpty())
}
