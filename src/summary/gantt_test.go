package summary

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// helper to create entries with times relative to a base
func entry(label, thread string, startOffset, endOffset time.Duration, failed bool) TimelineEntry {
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return TimelineEntry{
		Label:  label,
		Thread: thread,
		Start:  base.Add(startOffset),
		End:    base.Add(endOffset),
		Failed: failed,
	}
}

func TestRenderGanttEmpty(t *testing.T) {
	assert.Empty(t, RenderGantt(nil))
	assert.Empty(t, RenderGantt([]TimelineEntry{}))
}

func TestRenderGanttSingleThread(t *testing.T) {
	entries := []TimelineEntry{
		entry("go mod tidy", "main", 0, 850*time.Millisecond, false),
		entry("go vet", "main", 850*time.Millisecond, 2400*time.Millisecond, false),
	}

	result := RenderGantt(entries)

	assert.Contains(t, result, "```mermaid")
	assert.Contains(t, result, "gantt")
	assert.Contains(t, result, "section main")
	assert.Contains(t, result, "go mod tidy :done, t0, 0, 850")
	assert.Contains(t, result, "go vet :done, t1, 850, 2400")
	assert.Contains(t, result, "```\n")
	// Theme config
	assert.Contains(t, result, "doneTaskBkgColor")
	assert.Contains(t, result, "critBkgColor")
	assert.Contains(t, result, "barHeight")
}

func TestRenderGanttMultipleThreads(t *testing.T) {
	entries := []TimelineEntry{
		entry("go test", "main", time.Second, 5*time.Second, false),
		entry("Dep check", "deps", 0, 3*time.Second, false),
	}

	result := RenderGantt(entries)

	// "main" section should appear before "deps"
	mainIdx := strings.Index(result, "section main")
	depsIdx := strings.Index(result, "section deps")
	assert.Greater(t, depsIdx, mainIdx, "main section should come before deps")
}

func TestRenderGanttFailedStep(t *testing.T) {
	entries := []TimelineEntry{
		entry("go test", "main", 0, time.Second, true),
	}

	result := RenderGantt(entries)
	assert.Contains(t, result, ":crit,")
}

func TestRenderGanttLabelSanitization(t *testing.T) {
	entries := []TimelineEntry{
		entry("go build -o build/bin:thing", "main", 0, time.Second, false),
	}

	result := RenderGantt(entries)
	assert.NotContains(t, result, "bin:thing")
	assert.Contains(t, result, "bin thing")
}

func TestRenderGanttSortsWithinThread(t *testing.T) {
	entries := []TimelineEntry{
		entry("second", "main", 2*time.Second, 3*time.Second, false),
		entry("first", "main", time.Second, 2*time.Second, false),
	}

	result := RenderGantt(entries)

	firstIdx := strings.Index(result, "first")
	secondIdx := strings.Index(result, "second")
	assert.Greater(t, secondIdx, firstIdx, "entries should be sorted by start time")
}

func TestRenderGanttMinimumWidth(t *testing.T) {
	entries := []TimelineEntry{
		entry("setup", "main", 0, 5*time.Second, false),
		entry("instant", "main", time.Second, time.Second, false),
	}

	result := RenderGantt(entries)
	// Instant step (start=end=1s) should get minimum 100ms width: 1000, 1100
	assert.Contains(t, result, "1000, 1100")
}

func TestSanitizeLabel(t *testing.T) {
	assert.Equal(t, "foo bar", sanitizeLabel("foo:bar"))
	assert.Equal(t, "a b c", sanitizeLabel("a;b;c"))
	assert.Equal(t, "no hash", sanitizeLabel("no #hash"))
}

func TestRenderGanttWorkerThreadOrder(t *testing.T) {
	entries := []TimelineEntry{
		entry("linux/amd64", "worker-2", 0, time.Second, false),
		entry("linux/arm64", "worker-1", 0, time.Second, false),
		entry("go test", "main", 0, time.Second, false),
	}

	result := RenderGantt(entries)

	mainIdx := strings.Index(result, "section main")
	w1Idx := strings.Index(result, "section worker-1")
	w2Idx := strings.Index(result, "section worker-2")
	assert.Greater(t, w1Idx, mainIdx)
	assert.Greater(t, w2Idx, w1Idx)
}

func TestRenderGanttAxisFormatMinutes(t *testing.T) {
	entries := []TimelineEntry{
		entry("long step", "main", 0, 2*time.Minute, false),
	}
	result := RenderGantt(entries)
	assert.Contains(t, result, "axisFormat %M:%S")
}

func TestRenderGanttAxisFormatHours(t *testing.T) {
	entries := []TimelineEntry{
		entry("very long step", "main", 0, 2*time.Hour, false),
	}
	result := RenderGantt(entries)
	assert.Contains(t, result, "axisFormat %H:%M:%S")
}
