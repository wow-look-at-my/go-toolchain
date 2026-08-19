package summary

import (
	"fmt"
	"sort"
	"strings"
	"text/template"
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

	chart := ganttChart{AxisFormat: ganttAxisFormat(totalDuration)}

	taskID := 0
	for _, thread := range threads {
		tes := threadEntries[thread]
		// Sort by start time within each thread
		sort.Slice(tes, func(i, j int) bool {
			return tes[i].Start.Before(tes[j].Start)
		})

		section := ganttSection{Name: sanitizeLabel(thread)}
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
			section.Tasks = append(section.Tasks, ganttTask{
				Label: sanitizeLabel(e.Label) + " (" + fmtGanttDuration(e.End.Sub(e.Start)) + ")",
				Tag:   tag,
				ID:    taskID,
				Start: startMs,
				End:   endMs,
			})
			taskID++
		}
		chart.Sections = append(chart.Sections, section)
	}

	var sb strings.Builder
	if err := ganttTemplate.Execute(&sb, chart); err != nil {
		// The caller renders this into a summary, so say what broke there.
		// Silence would read as "the pipeline recorded no timeline".
		return fmt.Sprintf("gantt chart failed to render: %v\n", err)
	}
	return sb.String()
}

// ganttAxisFormat picks the axis unit the whole run fits in.
func ganttAxisFormat(total time.Duration) string {
	switch {
	case total >= time.Hour:
		return "%H:%M:%S"
	case total >= time.Minute:
		return "%M:%S"
	default:
		return "%S s"
	}
}

// The chart the template renders. Every field it names is here.
type (
	ganttChart struct {
		AxisFormat string
		Sections   []ganttSection
	}
	ganttSection struct {
		Name  string
		Tasks []ganttTask
	}
	ganttTask struct {
		Label string
		Tag   string
		ID    int
		Start int64
		End   int64
	}
)

// ganttTemplate is the chart itself, held as text. The theme block sets the
// colours of the done, active and crit bars. The literals are interpreted
// strings, not one raw string, because the mermaid fence is three backticks.
var ganttTemplate = template.Must(template.New("gantt").Parse(
	"```mermaid\n" +
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
		"    axisFormat {{.AxisFormat}}\n" +
		"{{range .Sections}}    section {{.Name}}\n" +
		"{{range .Tasks}}    {{.Label}} :{{.Tag}}, t{{.ID}}, {{.Start}}, {{.End}}\n" +
		"{{end}}{{end}}" +
		"```\n"))

// sanitizeLabel removes characters that Mermaid interprets as syntax
// and collapses any resulting whitespace runs.
func sanitizeLabel(s string) string {
	r := strings.NewReplacer(
		":", "",
		";", "",
		"#", "",
	)
	return strings.Join(strings.Fields(r.Replace(s)), " ")
}

// fmtGanttDuration formats a duration for display in Gantt bar labels.
func fmtGanttDuration(d time.Duration) string {
	switch {
	case d >= time.Minute:
		return fmt.Sprintf("%.1fm", d.Minutes())
	case d >= time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	default:
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
}
