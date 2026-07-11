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

// Span tracks a single in-progress timeline entry. Created by Timeline.Start.
type Span struct {
	tl     *Timeline
	label  string
	thread string
	start  time.Time
}

// Done completes the span and records it to the timeline.
func (s *Span) Done(failed bool) {
	s.tl.Record(s.label, s.thread, s.start, time.Now(), failed)
}

// Start begins tracking a timeline entry and returns a Span.
// Usage: defer tl.Start("step", "main").Done(false)
func (tl *Timeline) Start(label, thread string) *Span {
	return &Span{tl: tl, label: label, thread: thread, start: time.Now()}
}

// Entries returns a snapshot copy of all recorded entries.
func (tl *Timeline) Entries() []TimelineEntry {
	tl.mu.Lock()
	defer tl.mu.Unlock()
	out := make([]TimelineEntry, len(tl.entries))
	copy(out, tl.entries)
	return out
}
