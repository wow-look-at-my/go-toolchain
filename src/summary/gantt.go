package summary

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// RenderGantt produces a Mermaid Gantt chart from timeline entries.
// All entries are normalized relative to the earliest start time.
// Returns an empty string if there are no entries.
func RenderGantt(entries []TimelineEntry) string {
	if len(entries) == 0 {
		return ""
	}

	// Find the earliest start time to use as epoch
	epoch := entries[0].Start
	for _, e := range entries[1:] {
		if e.Start.Before(epoch) {
			epoch = e.Start
		}
	}

	// Group entries by thread
	threadEntries := make(map[string][]TimelineEntry)
	for _, e := range entries {
		threadEntries[e.Thread] = append(threadEntries[e.Thread], e)
	}

	// Sort threads: "main" first, then alphabetical
	threads := make([]string, 0, len(threadEntries))
	for t := range threadEntries {
		threads = append(threads, t)
	}
	sort.Slice(threads, func(i, j int) bool {
		if threads[i] == "main" {
			return true
		}
		if threads[j] == "main" {
			return false
		}
		return threads[i] < threads[j]
	})

	// Find total duration for axis format
	var maxEnd time.Time
	for _, e := range entries {
		if e.End.After(maxEnd) {
			maxEnd = e.End
		}
	}
	totalDuration := maxEnd.Sub(epoch)

	var sb strings.Builder
	sb.WriteString("```mermaid\ngantt\n")
	sb.WriteString("    title Pipeline Timeline\n")
	sb.WriteString("    dateFormat x\n")
	// Pick axis format based on total duration
	if totalDuration >= time.Hour {
		sb.WriteString("    axisFormat %H:%M:%S\n")
	} else if totalDuration >= time.Minute {
		sb.WriteString("    axisFormat %M:%S\n")
	} else {
		sb.WriteString("    axisFormat %S s\n")
	}

	taskID := 0
	for _, thread := range threads {
		tes := threadEntries[thread]
		// Sort by start time within each thread
		sort.Slice(tes, func(i, j int) bool {
			return tes[i].Start.Before(tes[j].Start)
		})

		sb.WriteString(fmt.Sprintf("    section %s\n", sanitizeLabel(thread)))

		for _, e := range tes {
			tag := "done"
			if e.Failed {
				tag = "crit"
			}
			startMs := e.Start.Sub(epoch).Milliseconds()
			endMs := e.End.Sub(epoch).Milliseconds()
			// Ensure minimum 100ms width so the bar is visible and labeled
			if endMs-startMs < 100 {
				endMs = startMs + 100
			}
			sb.WriteString(fmt.Sprintf("    %s :%s, t%d, %d, %d\n",
				sanitizeLabel(e.Label), tag, taskID, startMs, endMs))
			taskID++
		}
	}

	sb.WriteString("```\n")
	return sb.String()
}

// sanitizeLabel removes characters that Mermaid interprets as syntax.
func sanitizeLabel(s string) string {
	r := strings.NewReplacer(
		":", " ",
		";", " ",
		"#", "",
	)
	return r.Replace(s)
}
