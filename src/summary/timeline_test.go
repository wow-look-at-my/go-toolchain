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
	epoch := tl.epoch

	start := epoch.Add(100 * time.Millisecond)
	end := epoch.Add(500 * time.Millisecond)
	tl.Record("go mod tidy", "main", start, end, false)

	entries := tl.Entries()
	require.Len(t, entries, 1)
	assert.Equal(t, "go mod tidy", entries[0].Label)
	assert.Equal(t, "main", entries[0].Thread)
	assert.Equal(t, 100*time.Millisecond, entries[0].Start)
	assert.Equal(t, 500*time.Millisecond, entries[0].End)
	assert.False(t, entries[0].Failed)
}

func TestTimelineRecordFailed(t *testing.T) {
	tl := NewTimeline()
	epoch := tl.epoch

	tl.Record("go test", "main", epoch, epoch.Add(time.Second), true)

	entries := tl.Entries()
	require.Len(t, entries, 1)
	assert.True(t, entries[0].Failed)
}

func TestTimelineEntriesIsCopy(t *testing.T) {
	tl := NewTimeline()
	epoch := tl.epoch
	tl.Record("step1", "main", epoch, epoch.Add(time.Second), false)

	entries := tl.Entries()
	entries[0].Label = "modified"

	// Original should be unchanged
	assert.Equal(t, "step1", tl.Entries()[0].Label)
}

func TestTimelineConcurrentRecording(t *testing.T) {
	tl := NewTimeline()
	epoch := tl.epoch

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			start := epoch.Add(time.Duration(n) * time.Millisecond)
			end := start.Add(10 * time.Millisecond)
			tl.Record("task", "worker", start, end, false)
		}(i)
	}
	wg.Wait()

	assert.Len(t, tl.Entries(), 100)
}
