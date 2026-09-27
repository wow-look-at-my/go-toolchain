package profile

import (
	"fmt"
	"path"
	"sort"
	"time"

	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
)

// maxTraceLanes caps "go actions #NN" lanes; spill-over clamps to the earliest-free lane.
const maxTraceLanes = 32

// AddTraceEvents records a timed event per executed action into tr,
// assigning concurrent actions to numbered lanes with a greedy interval
// scheduler — the Chrome writer clamps overlapping events within a single
// thread, so without lanes a parallel compile phase would collapse into a
// serialized smear. The event args carry the package, mode and action ID, so
// clicking a bar in chrome://tracing answers "what was this and why did it
// run".
func AddTraceEvents(tr *gotrace.Trace, actions []Action) {
	if tr == nil {
		return
	}
	executed := make([]Action, 0, len(actions))
	for _, a := range actions {
		if a.Executed() && a.Wall() > 0 {
			executed = append(executed, a)
		}
	}
	sort.Slice(executed, func(i, j int) bool {
		return executed[i].TimeStart.Before(executed[j].TimeStart)
	})

	var laneEnds []time.Time
	for _, a := range executed {
		lane := -1
		for i := range laneEnds {
			if !laneEnds[i].After(a.TimeStart) {
				lane = i
				break
			}
		}
		if lane < 0 {
			if len(laneEnds) < maxTraceLanes {
				laneEnds = append(laneEnds, time.Time{})
				lane = len(laneEnds) - 1
			} else {
				// All lanes busy: spill onto the lane that frees up earliest.
				lane = 0
				for i := range laneEnds {
					if laneEnds[i].Before(laneEnds[lane]) {
						lane = i
					}
				}
			}
		}
		laneEnds[lane] = a.TimeDone

		args := map[string]string{"mode": a.Mode}
		if a.Package != "" {
			args["package"] = a.Package
		}
		if a.ActionID != "" {
			args["action_id"] = a.ActionID
		}
		tr.Record(gotrace.Event{
			Name:     traceName(a),
			Category: "action",
			Thread:   fmt.Sprintf("go actions #%02d", lane+1),
			Start:    a.TimeStart,
			End:      a.TimeDone,
			Args:     args,
		})
	}
}

// traceName is the short display name for an action bar: the package's base
// name, suffixed with the mode for non-compile actions.
func traceName(a Action) string {
	name := path.Base(a.Package)
	if name == "." || name == "/" || a.Package == "" {
		return a.Mode
	}
	if a.Mode != "" && a.Mode != "build" {
		return name + " (" + a.Mode + ")"
	}
	return name
}
