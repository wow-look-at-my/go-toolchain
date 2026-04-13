package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/summary"
)

// chromeEvent is a Chrome Trace Event Format event.
// Spec: https://docs.google.com/document/d/1CvAClvFfyA5R-PhYUmn5OOQtYMH4h6I0nSsKchNAySU
type chromeEvent struct {
	Name string      `json:"name"`
	Cat  string      `json:"cat,omitempty"`
	Ph   string      `json:"ph"`            // B=begin, E=end, X=complete, M=metadata, I=instant
	Ts   int64       `json:"ts"`            // microseconds since epoch
	Dur  int64       `json:"dur,omitempty"` // microseconds (for ph=X)
	Pid  int         `json:"pid"`
	Tid  int         `json:"tid"`
	Args interface{} `json:"args,omitempty"`
}

// Event is a single trace event to be written to the Chrome trace file.
type Event struct {
	Name     string
	Category string // e.g. "pipeline", "parse", "analyze", "test", "compile"
	Thread   string // lane in the trace viewer
	Start    time.Time
	End      time.Time
	Failed   bool
	Args     map[string]string // arbitrary key-value metadata
}

// Trace collects events from across the pipeline for Chrome trace export.
type Trace struct {
	mu     sync.Mutex
	events []Event
}

// NewTrace creates an empty trace collector.
func NewTrace() *Trace {
	return &Trace{}
}

// Record adds a completed event.
func (t *Trace) Record(ev Event) {
	t.mu.Lock()
	t.events = append(t.events, ev)
	t.mu.Unlock()
}

// Complete records a simple completed event.
func (t *Trace) Complete(name, category, thread string, start, end time.Time) {
	t.Record(Event{Name: name, Category: category, Thread: thread, Start: start, End: end})
}

// threadID assigns a stable numeric ID to each thread name.
func resolveThreadID(name string, seen map[string]int) int {
	if id, ok := seen[name]; ok {
		return id
	}
	id := len(seen) + 1
	seen[name] = id
	return id
}

// WriteChrome writes all collected events plus pipeline timeline entries as a
// Chrome trace JSON file. Loadable in chrome://tracing or DevTools Performance tab.
func WriteChrome(path string, timeline []summary.TimelineEntry, trace *Trace) error {
	threads := make(map[string]int)
	var out []chromeEvent

	// Gather all thread names.
	for _, e := range timeline {
		resolveThreadID(e.Thread, threads)
	}
	if trace != nil {
		trace.mu.Lock()
		for _, e := range trace.events {
			resolveThreadID(e.Thread, threads)
		}
		trace.mu.Unlock()
	}

	// Metadata events.
	for name, tid := range threads {
		out = append(out, chromeEvent{
			Name: "thread_name", Ph: "M", Pid: 1, Tid: tid,
			Args: map[string]string{"name": name},
		})
	}
	out = append(out, chromeEvent{
		Name: "process_name", Ph: "M", Pid: 1, Tid: 0,
		Args: map[string]string{"name": "go-toolchain"},
	})

	// Pipeline step events (from the step system).
	for _, e := range timeline {
		tid := resolveThreadID(e.Thread, threads)
		dur := e.End.Sub(e.Start).Microseconds()
		if dur < 1 {
			dur = 1
		}
		ev := chromeEvent{
			Name: e.Label, Cat: "pipeline", Ph: "X",
			Ts: e.Start.UnixMicro(), Dur: dur, Pid: 1, Tid: tid,
		}
		if e.Failed {
			ev.Args = map[string]string{"status": "failed"}
		}
		out = append(out, ev)
	}

	// Fine-grained events (from instrumented code).
	if trace != nil {
		trace.mu.Lock()
		for _, e := range trace.events {
			tid := resolveThreadID(e.Thread, threads)
			dur := e.End.Sub(e.Start).Microseconds()
			if dur < 1 {
				dur = 1
			}
			ev := chromeEvent{
				Name: e.Name, Cat: e.Category, Ph: "X",
				Ts: e.Start.UnixMicro(), Dur: dur, Pid: 1, Tid: tid,
			}
			args := e.Args
			if e.Failed {
				if args == nil {
					args = make(map[string]string)
				}
				args["status"] = "failed"
			}
			if len(args) > 0 {
				ev.Args = args
			}
			out = append(out, ev)
		}
		trace.mu.Unlock()
	}

	if len(out) == 0 {
		return nil
	}

	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}
