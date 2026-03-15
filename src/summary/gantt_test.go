package summary

import (
	"strings"
	"testing"
	"time"

	"github.com/wow-look-at-my/testify/assert"
)

func TestRenderGanttEmpty(t *testing.T) {
	assert.Empty(t, RenderGantt(nil))
	assert.Empty(t, RenderGantt([]TimelineEntry{}))
}

func TestRenderGanttSingleThread(t *testing.T) {
	entries := []TimelineEntry{
		{Label: "go mod tidy", Thread: "main", Start: 0, End: 850 * time.Millisecond},
		{Label: "go vet", Thread: "main", Start: 850 * time.Millisecond, End: 2400 * time.Millisecond},
	}

	result := RenderGantt(entries)

	assert.Contains(t, result, "```mermaid")
	assert.Contains(t, result, "gantt")
	assert.Contains(t, result, "section main")
	assert.Contains(t, result, "go mod tidy :done, t0, 0, 850")
	assert.Contains(t, result, "go vet :done, t1, 850, 2400")
	assert.Contains(t, result, "```\n")
}

func TestRenderGanttMultipleThreads(t *testing.T) {
	entries := []TimelineEntry{
		{Label: "go test", Thread: "main", Start: time.Second, End: 5 * time.Second},
		{Label: "Dep check", Thread: "deps", Start: 0, End: 3 * time.Second},
	}

	result := RenderGantt(entries)

	// "main" section should appear before "deps"
	mainIdx := strings.Index(result, "section main")
	depsIdx := strings.Index(result, "section deps")
	assert.Greater(t, depsIdx, mainIdx, "main section should come before deps")
}

func TestRenderGanttFailedStep(t *testing.T) {
	entries := []TimelineEntry{
		{Label: "go test", Thread: "main", Start: 0, End: time.Second, Failed: true},
	}

	result := RenderGantt(entries)
	assert.Contains(t, result, ":crit,")
}

func TestRenderGanttLabelSanitization(t *testing.T) {
	entries := []TimelineEntry{
		{Label: "go build -o build/bin:thing", Thread: "main", Start: 0, End: time.Second},
	}

	result := RenderGantt(entries)
	// Colons should be replaced
	assert.NotContains(t, result, "bin:thing")
	assert.Contains(t, result, "bin thing")
}

func TestRenderGanttSortsWithinThread(t *testing.T) {
	entries := []TimelineEntry{
		{Label: "second", Thread: "main", Start: 2 * time.Second, End: 3 * time.Second},
		{Label: "first", Thread: "main", Start: time.Second, End: 2 * time.Second},
	}

	result := RenderGantt(entries)

	firstIdx := strings.Index(result, "first")
	secondIdx := strings.Index(result, "second")
	assert.Greater(t, secondIdx, firstIdx, "entries should be sorted by start time")
}

func TestRenderGanttMinimumWidth(t *testing.T) {
	entries := []TimelineEntry{
		{Label: "instant", Thread: "main", Start: time.Second, End: time.Second},
	}

	result := RenderGantt(entries)
	// Should have endMs = startMs + 1 to be visible
	assert.Contains(t, result, "1000, 1001")
}

func TestSanitizeLabel(t *testing.T) {
	assert.Equal(t, "foo bar", sanitizeLabel("foo:bar"))
	assert.Equal(t, "a b c", sanitizeLabel("a;b;c"))
	assert.Equal(t, "no hash", sanitizeLabel("no #hash"))
}

func TestRenderGanttWorkerThreadOrder(t *testing.T) {
	entries := []TimelineEntry{
		{Label: "linux/amd64", Thread: "worker-2", Start: 0, End: time.Second},
		{Label: "linux/arm64", Thread: "worker-1", Start: 0, End: time.Second},
		{Label: "go test", Thread: "main", Start: 0, End: time.Second},
	}

	result := RenderGantt(entries)

	mainIdx := strings.Index(result, "section main")
	w1Idx := strings.Index(result, "section worker-1")
	w2Idx := strings.Index(result, "section worker-2")
	assert.Greater(t, w1Idx, mainIdx)
	assert.Greater(t, w2Idx, w1Idx)
}
