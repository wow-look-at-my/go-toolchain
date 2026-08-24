package profile

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

// Action is one row of cmd/go's -debug-actiongraph JSON dump. Only the fields
// the profiler consumes are declared; unknown fields are ignored and absent
// ones stay zero, so parsing stays compatible across cmd/go versions.
//
// ActionID is the 20-char base64.RawURLEncoding(wireActionID[:15]) truncated
// cache key, byte-identical to what the cacheprog emits in its stat events --
// the join key between "what did the build do" and "what did the cache do".
type Action struct {
	ID        int       `json:"ID"`
	Mode      string    `json:"Mode"`
	Package   string    `json:"Package"`
	NeedBuild bool      `json:"NeedBuild"`
	ActionID  string    `json:"ActionID"`
	TimeReady time.Time `json:"TimeReady"`
	TimeStart time.Time `json:"TimeStart"`
	TimeDone  time.Time `json:"TimeDone"`
	CmdReal   int64     `json:"CmdReal"` // ns spent in spawned commands
	CmdUser   int64     `json:"CmdUser"` // ns
	CmdSys    int64     `json:"CmdSys"`  // ns
	Target    string    `json:"Target"`
}

// Executed reports whether cmd/go stamped a start and completion time; cache-satisfied actions carry zero times.
func (a *Action) Executed() bool {
	return !a.TimeStart.IsZero() && !a.TimeDone.IsZero() && !a.TimeDone.Before(a.TimeStart)
}

// Wall is the action's wall-clock duration (TimeDone - TimeStart), zero for
// actions that never executed.
func (a *Action) Wall() time.Duration {
	if !a.Executed() {
		return 0
	}
	return a.TimeDone.Sub(a.TimeStart)
}

// LoadGraphs parses every actiongraph dump and merges rows sharing an
// ActionID (the executed instance wins). Best-effort: a missing file is
// skipped silently, a malformed one warns once -- the profile must never
// fail a build.
func LoadGraphs(files []string, warn io.Writer) []Action {
	var all []Action
	for _, path := range files {
		actions, err := loadGraphFile(path)
		if err != nil {
			if !os.IsNotExist(err) && warn != nil {
				fmt.Fprintf(warn, "⇒ Warning: build profile: skipping %s: %v\n", path, err)
			}
			continue
		}
		all = append(all, actions...)
	}
	return mergeActions(all)
}

func loadGraphFile(path string) ([]Action, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var actions []Action
	if err := json.Unmarshal(data, &actions); err != nil {
		return nil, fmt.Errorf("parse actiongraph: %w", err)
	}
	return actions, nil
}

// mergeActions dedupes rows by ActionID, preferring the instance that
// actually executed (and, between two executed instances, the longer one —
// the run that did the work). Rows without an ActionID cannot alias and are
// kept as-is.
func mergeActions(all []Action) []Action {
	out := make([]Action, 0, len(all))
	byID := make(map[string]int, len(all))
	for _, a := range all {
		if a.ActionID == "" {
			out = append(out, a)
			continue
		}
		i, seen := byID[a.ActionID]
		if !seen {
			byID[a.ActionID] = len(out)
			out = append(out, a)
			continue
		}
		if preferAction(a, out[i]) {
			out[i] = a
		}
	}
	return out
}

// preferAction reports whether a should replace b as the representative
// instance of the same ActionID.
func preferAction(a, b Action) bool {
	if a.Executed() != b.Executed() {
		return a.Executed()
	}
	return a.Wall() > b.Wall()
}
