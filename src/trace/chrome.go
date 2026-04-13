package trace

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/go-toolchain/src/summary"
)

// traceEvent is a Chrome Trace Event Format event.
// Spec: https://docs.google.com/document/d/1CvAClvFfyA5R-PhYUmn5OOQtYMH4h6I0nSsKchNAySU
type traceEvent struct {
	Name string      `json:"name"`
	Cat  string      `json:"cat,omitempty"`
	Ph   string      `json:"ph"`           // B=begin, E=end, X=complete
	Ts   int64       `json:"ts"`           // microseconds since epoch
	Dur  int64       `json:"dur,omitempty"`// microseconds (for ph=X)
	Pid  int         `json:"pid"`
	Tid  int         `json:"tid"`
	Args interface{} `json:"args,omitempty"`
}

// threadID assigns a stable numeric ID to each thread name.
func threadID(name string, seen map[string]int) int {
	if id, ok := seen[name]; ok {
		return id
	}
	id := len(seen) + 1
	seen[name] = id
	return id
}

// WriteChrome writes pipeline timeline entries as a Chrome trace JSON file.
// The file can be loaded in chrome://tracing or Chrome DevTools Performance tab.
func WriteChrome(path string, entries []summary.TimelineEntry) error {
	if len(entries) == 0 {
		return nil
	}

	threads := make(map[string]int)
	var events []traceEvent

	// Add thread metadata events so the UI shows thread names.
	for _, e := range entries {
		threadID(e.Thread, threads)
	}
	for name, tid := range threads {
		events = append(events, traceEvent{
			Name: "thread_name",
			Ph:   "M", // metadata
			Pid:  1,
			Tid:  tid,
			Args: map[string]string{"name": name},
		})
	}

	// Add process metadata.
	events = append(events, traceEvent{
		Name: "process_name",
		Ph:   "M",
		Pid:  1,
		Tid:  0,
		Args: map[string]string{"name": "go-toolchain"},
	})

	// Convert timeline entries to complete (X) events.
	for _, e := range entries {
		tid := threadID(e.Thread, threads)
		dur := e.End.Sub(e.Start).Microseconds()
		if dur < 1 {
			dur = 1
		}
		ev := traceEvent{
			Name: e.Label,
			Cat:  "pipeline",
			Ph:   "X",
			Ts:   e.Start.UnixMicro(),
			Dur:  dur,
			Pid:  1,
			Tid:  tid,
		}
		if e.Failed {
			ev.Args = map[string]string{"status": "failed"}
		}
		events = append(events, ev)
	}

	os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(events)
}
