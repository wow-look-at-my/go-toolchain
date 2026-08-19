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
	assert.Contains(t, result, "go mod tidy (850ms) :done, t0, 0, 850")
	assert.Contains(t, result, "go vet (1.6s) :done, t1, 850, 2400")
	assert.Contains(t, result, "```\n")
	// Theme config
	assert.Contains(t, result, "doneTaskBkgColor")
	assert.Contains(t, result, "critBkgColor")
	assert.Contains(t, result, "barHeight")
}

// TestRenderGanttRendersTheWholeDocument pins the chart byte for byte. The
// Contains assertions around it would all pass with a stray blank line between
// two sections, which mermaid reads as the end of the chart.
func TestRenderGanttRendersTheWholeDocument(t *testing.T) {
	entries := []TimelineEntry{
		entry("go vet", "main", 0, time.Second, false),
		entry("go test", "main", time.Second, 2*time.Second, true),
		entry("Dep check", "deps", 0, 3*time.Second, false),
	}

	const want = "```mermaid\n" +
		"---\n" +
		"config:\n" +
		"  theme: base\n" +
		"  themeVariables:\n" +
		"    primaryColor: \"#4a90d9\"\n" +
		"    primaryTextColor: \"#fff\"\n" +
		"    primaryBorderColor: \"#2a6cb0\"\n" +
		"    doneTaskBkgColor: \"#2ea44f\"\n" +
		"    doneTaskBorderColor: \"#22863a\"\n" +
		"    critBkgColor: \"#d73a49\"\n" +
		"    critBorderColor: \"#b31d28\"\n" +
		"    activeTaskBkgColor: \"#6f42c1\"\n" +
		"    activeTaskBorderColor: \"#5a32a3\"\n" +
		"    sectionBkgColor: \"#f6f8fa\"\n" +
		"    altSectionBkgColor: \"#eef1f5\"\n" +
		"    gridColor: \"#d0d7de\"\n" +
		"    taskTextColor: \"#fff\"\n" +
		"    taskTextOutsideColor: \"#24292f\"\n" +
		"    sectionFontSize: 14\n" +
		"  gantt:\n" +
		"    barHeight: 28\n" +
		"    fontSize: 13\n" +
		"---\n" +
		"gantt\n" +
		"    title Pipeline Timeline\n" +
		"    dateFormat x\n" +
		"    axisFormat %S s\n" +
		"    section main\n" +
		"    go vet :done, t0, 0, 1000\n" +
		"    go test :crit, t1, 1000, 2000\n" +
		"    section deps\n" +
		"    Dep check :done, t2, 0, 3000\n" +
		"```\n"

	assert.Equal(t, want, RenderGantt(entries))
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
	assert.Contains(t, result, "build/binthing")
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
	assert.Equal(t, "foobar", sanitizeLabel("foo:bar"))
	assert.Equal(t, "abc", sanitizeLabel("a;b;c"))
	assert.Equal(t, "no hash", sanitizeLabel("no #hash"))
	assert.Equal(t, "vet compile", sanitizeLabel("vet: compile"))
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
