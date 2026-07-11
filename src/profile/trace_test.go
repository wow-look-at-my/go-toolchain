package profile

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/cache"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
)

func TestAddTraceEvents_LanesAndArgs(t *testing.T) {
	t0 := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
	actions := []Action{
		// Two overlapping compiles → two lanes; a third after both → lane 1 reused.
		{Mode: "build", Package: "example.com/m/a", ActionID: "aaaaaaaaaaaaaaaaaaaa",
			TimeStart: t0, TimeDone: t0.Add(2 * time.Second)},
		{Mode: "build", Package: "example.com/m/b", ActionID: "bbbbbbbbbbbbbbbbbbbb",
			TimeStart: t0.Add(time.Second), TimeDone: t0.Add(3 * time.Second)},
		{Mode: "link", Package: "example.com/m", ActionID: "cccccccccccccccccccc",
			TimeStart: t0.Add(4 * time.Second), TimeDone: t0.Add(5 * time.Second)},
		// Never executed: no event.
		{Mode: "build", Package: "example.com/m/skip", ActionID: "dddddddddddddddddddd"},
	}
	outcomes := map[string]cache.ActionOutcome{
		"aaaaaaaaaaaaaaaaaaaa": {Get: "miss", Put: true},
	}

	tr := gotrace.NewTrace()
	AddTraceEvents(tr, actions, outcomes)
	events := tr.Events()
	require.Len(t, events, 3)

	byName := map[string]gotrace.Event{}
	for _, ev := range events {
		byName[ev.Name] = ev
	}
	a, b, link := byName["a"], byName["b"], byName["m (link)"]
	assert.Equal(t, "go actions #01", a.Thread)
	assert.Equal(t, "go actions #02", b.Thread, "overlapping actions must land on distinct lanes")
	assert.Equal(t, "go actions #01", link.Thread, "a later action reuses the freed lane")
	assert.Equal(t, "action", a.Category)
	assert.Equal(t, "miss+put", a.Args["cache"])
	assert.Equal(t, "example.com/m/a", a.Args["package"])
	assert.Equal(t, "aaaaaaaaaaaaaaaaaaaa", a.Args["action_id"])
	_, hasCache := b.Args["cache"]
	assert.False(t, hasCache, "no observed outcome: no cache arg")
}

func TestAddTraceEvents_NilTraceAndLaneSpill(t *testing.T) {
	AddTraceEvents(nil, testActions(), nil) // must not panic

	// More concurrent actions than lanes: all still recorded, spilling onto
	// the earliest-free lane.
	t0 := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
	var actions []Action
	for i := 0; i < maxTraceLanes+5; i++ {
		actions = append(actions, Action{
			Mode: "build", Package: "p",
			TimeStart: t0.Add(time.Duration(i) * time.Millisecond),
			TimeDone:  t0.Add(10 * time.Second),
		})
	}
	tr := gotrace.NewTrace()
	AddTraceEvents(tr, actions, nil)
	events := tr.Events()
	assert.Len(t, events, maxTraceLanes+5)
	lanes := map[string]bool{}
	for _, ev := range events {
		lanes[ev.Thread] = true
	}
	assert.Len(t, lanes, maxTraceLanes, "spill must not create lanes past the cap")
}

func TestTraceName(t *testing.T) {
	assert.Equal(t, "pkga", traceName(Action{Mode: "build", Package: "example.com/m/pkga"}))
	assert.Equal(t, "m (link)", traceName(Action{Mode: "link", Package: "example.com/m"}))
	assert.Equal(t, "go build", traceName(Action{Mode: "go build"}))
	assert.Equal(t, "pkga (test run)", traceName(Action{Mode: "test run", Package: "example.com/m/pkga"}))
}
