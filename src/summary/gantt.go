package summary

import (
	"fmt"
	"sort"
	"strings"
)

// RenderGantt produces a Mermaid Gantt chart from timeline entries.
// Returns an empty string if there are no entries.
func RenderGantt(entries []TimelineEntry) string {
	if len(entries) == 0 {
		return ""
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

	var sb strings.Builder
	sb.WriteString("```mermaid\ngantt\n")
	sb.WriteString("    title Pipeline Timeline\n")
	sb.WriteString("    dateFormat x\n")
	sb.WriteString("    axisFormat %Ss\n")

	taskID := 0
	for _, thread := range threads {
		entries := threadEntries[thread]
		// Sort by start time within each thread
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Start < entries[j].Start
		})

		sb.WriteString(fmt.Sprintf("    section %s\n", sanitizeLabel(thread)))

		for _, e := range entries {
			tag := "done"
			if e.Failed {
				tag = "crit"
			}
			startMs := e.Start.Milliseconds()
			endMs := e.End.Milliseconds()
			// Ensure minimum 1ms width so the bar is visible
			if endMs <= startMs {
				endMs = startMs + 1
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
