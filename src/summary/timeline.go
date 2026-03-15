package summary

import (
	"sync"
	"time"
)

// TimelineEntry records a single action in the pipeline timeline.
// Start and End are absolute wall-clock times; normalization happens at render time.
type TimelineEntry struct {
	Label  string
	Thread string    // e.g. "main", "deps", "worker-1"
	Start  time.Time // absolute wall clock
	End    time.Time // absolute wall clock
	Failed bool
}

// Timeline is a goroutine-safe collector of pipeline action timings.
type Timeline struct {
	mu      sync.Mutex
	entries []TimelineEntry
}

// NewTimeline creates an empty Timeline.
func NewTimeline() *Timeline {
	return &Timeline{}
}

// Record adds a completed action to the timeline.
func (tl *Timeline) Record(label, thread string, start, end time.Time, failed bool) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.entries = append(tl.entries, TimelineEntry{
		Label:  label,
		Thread: thread,
		Start:  start,
		End:    end,
		Failed: failed,
	})
}

// Entries returns a snapshot copy of all recorded entries.
func (tl *Timeline) Entries() []TimelineEntry {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	out := make([]TimelineEntry, len(tl.entries))
	copy(out, tl.entries)
	return out
}
