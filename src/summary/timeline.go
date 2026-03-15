package summary

import (
	"sync"
	"time"
)

// TimelineEntry records a single action in the pipeline timeline.
type TimelineEntry struct {
	Label  string
	Thread string        // e.g. "main", "deps", "worker-1"
	Start  time.Duration // relative to pipeline start
	End    time.Duration // relative to pipeline start
	Failed bool
}

// Timeline is a goroutine-safe collector of pipeline action timings.
type Timeline struct {
	mu      sync.Mutex
	epoch   time.Time
	entries []TimelineEntry
}

// NewTimeline creates a Timeline anchored to the current time.
func NewTimeline() *Timeline {
	return &Timeline{epoch: time.Now()}
}

// Record adds a completed action to the timeline. start and end are absolute
// wall-clock times; they are converted to durations relative to the epoch.
func (tl *Timeline) Record(label, thread string, start, end time.Time, failed bool) {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	tl.entries = append(tl.entries, TimelineEntry{
		Label:  label,
		Thread: thread,
		Start:  start.Sub(tl.epoch),
		End:    end.Sub(tl.epoch),
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
