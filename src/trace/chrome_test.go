package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/summary"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestWriteChrome(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []summary.TimelineEntry{
		{Label: "go mod tidy", Thread: "main", Start: t0, End: t0.Add(2 * time.Second)},
		{Label: "go vet", Thread: "main", Start: t0.Add(2 * time.Second), End: t0.Add(5 * time.Second)},
		{Label: "tests", Thread: "test", Start: t0.Add(5 * time.Second), End: t0.Add(15 * time.Second), Failed: true},
	}

	tr := NewTrace()
	tr.Complete("compile pkg/foo", "compile", "main", t0.Add(15*time.Second), t0.Add(16*time.Second))
	tr.Record(Event{
		Name: "failed step", Category: "build", Thread: "main",
		Start: t0.Add(16 * time.Second), End: t0.Add(17 * time.Second),
		Failed: true, Args: map[string]string{"reason": "exit 1"},
	})

	err := WriteChrome(path, entries, tr)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var events []chromeEvent
	require.NoError(t, json.Unmarshal(data, &events))

	// Should have: 2 thread metadata + 1 process metadata + 3 timeline + 2 trace events = 8
	assert.Len(t, events, 8)

	// Check that the failed timeline entry has status=failed in args
	var foundFailed bool
	for _, e := range events {
		if e.Name == "tests" {
			args, ok := e.Args.(map[string]interface{})
			require.True(t, ok)
			assert.Equal(t, "failed", args["status"])
			foundFailed = true
		}
	}
	assert.True(t, foundFailed, "should have found failed timeline entry")
}

func TestWriteChromeNilTrace(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []summary.TimelineEntry{
		{Label: "step", Thread: "main", Start: t0, End: t0.Add(time.Second)},
	}

	err := WriteChrome(path, entries, nil)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var events []chromeEvent
	require.NoError(t, json.Unmarshal(data, &events))
	assert.NotEmpty(t, events)
}

func TestWriteChromeEmptyInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")

	err := WriteChrome(path, nil, nil)
	require.NoError(t, err)

	// Even with no timeline/trace data, the process_name metadata is always written
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var events []chromeEvent
	require.NoError(t, json.Unmarshal(data, &events))
	assert.Len(t, events, 1)
	assert.Equal(t, "process_name", events[0].Name)
}

func TestWriteChromeOverlappingEvents(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "trace.json")

	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entries := []summary.TimelineEntry{
		{Label: "step", Thread: "main", Start: t0, End: t0.Add(time.Second)},
	}

	tr := NewTrace()
	// Two overlapping events on the same thread — WriteChrome should clamp
	tr.Complete("a", "cat", "main", t0, t0.Add(5*time.Second))
	tr.Complete("b", "cat", "main", t0.Add(2*time.Second), t0.Add(7*time.Second))

	err := WriteChrome(path, entries, tr)
	require.NoError(t, err)

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var events []chromeEvent
	require.NoError(t, json.Unmarshal(data, &events))

	// Find event "b" — its start should be clamped to after "a" ends
	var foundB bool
	for _, e := range events {
		if e.Name == "b" {
			foundB = true
			assert.True(t, e.Ts >= t0.Add(5*time.Second).UnixMicro(),
				"overlapping event should be clamped: ts=%d", e.Ts)
		}
	}
	require.True(t, foundB, "event 'b' not found in output")
}
