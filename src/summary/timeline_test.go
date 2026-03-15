package summary

import (
	"sync"
	"testing"
	"time"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestNewTimeline(t *testing.T) {
	tl := NewTimeline()
	require.NotNil(t, tl)
	assert.Empty(t, tl.Entries())
}

func TestTimelineRecord(t *testing.T) {
	tl := NewTimeline()

	t0 := time.Now()
	start := t0.Add(100 * time.Millisecond)
	end := t0.Add(500 * time.Millisecond)
	tl.Record("go mod tidy", "main", start, end, false)

	entries := tl.Entries()
	require.Len(t, entries, 1)
	assert.Equal(t, "go mod tidy", entries[0].Label)
	assert.Equal(t, "main", entries[0].Thread)
	assert.Equal(t, start, entries[0].Start)
	assert.Equal(t, end, entries[0].End)
	assert.False(t, entries[0].Failed)
}

func TestTimelineRecordFailed(t *testing.T) {
	tl := NewTimeline()

	t0 := time.Now()
	tl.Record("go test", "main", t0, t0.Add(time.Second), true)

	entries := tl.Entries()
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Failed)
}

func TestTimelineEntriesIsCopy(t *testing.T) {
	tl := NewTimeline()
	t0 := time.Now()
	tl.Record("step1", "main", t0, t0.Add(time.Second), false)

	entries := tl.Entries()
	entries[0].Label = "modified"

	// Original should be unchanged
	assert.Equal(t, "step1", tl.Entries()[0].Label)
}

func TestTimelineConcurrentRecording(t *testing.T) {
	tl := NewTimeline()
	t0 := time.Now()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			start := t0.Add(time.Duration(n) * time.Millisecond)
			end := start.Add(10 * time.Millisecond)
			tl.Record("task", "worker", start, end, false)
		}(i)
	}
	wg.Wait()

	assert.Len(t, tl.Entries(), 100)
}
