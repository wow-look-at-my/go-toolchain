package profile

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReportSchema versions the profile.json layout. Bump on incompatible change.
const ReportSchema = 2

// Row is an actiongraph action's timing.
type Row struct {
	ActionID  string  `json:"action_id,omitempty"`
	Package   string  `json:"package,omitempty"`
	Mode      string  `json:"mode,omitempty"`
	NeedBuild bool    `json:"need_build,omitempty"`
	Start     string  `json:"start,omitempty"` // RFC3339Nano; executed actions only
	WallMS    float64 `json:"wall_ms,omitempty"`
	CmdRealMS float64 `json:"cmd_real_ms,omitempty"`
	CmdUserMS float64 `json:"cmd_user_ms,omitempty"`
	CmdSysMS  float64 `json:"cmd_sys_ms,omitempty"`
}

// Report is the build profile written to build/profile.json. WallMSTotal sums
// per-action wall time without folding out parallelism, and Actions sorts by
// wall time descending. No field carries a cache hit or miss.
type Report struct {
	Schema          int       `json:"schema"`
	Created         time.Time `json:"created"`
	TotalActions    int       `json:"total_actions"`
	ExecutedActions int       `json:"executed_actions"`
	WallMSTotal     float64   `json:"wall_ms_total"`
	Actions         []Row     `json:"actions"`
}

// BuildReport turns the merged actiongraph into a timing report.
func BuildReport(actions []Action) *Report {
	r := &Report{
		Schema:       ReportSchema,
		Created:      time.Now().UTC(),
		TotalActions: len(actions),
		Actions:      make([]Row, 0, len(actions)),
	}
	const nsPerMS = float64(time.Millisecond)
	for i := range actions {
		a := &actions[i]
		row := Row{
			ActionID:  a.ActionID,
			Package:   a.Package,
			Mode:      a.Mode,
			NeedBuild: a.NeedBuild,
			WallMS:    float64(a.Wall()) / nsPerMS,
			CmdRealMS: float64(a.CmdReal) / nsPerMS,
			CmdUserMS: float64(a.CmdUser) / nsPerMS,
			CmdSysMS:  float64(a.CmdSys) / nsPerMS,
		}
		if a.Executed() {
			r.ExecutedActions++
			row.Start = a.TimeStart.Format(time.RFC3339Nano)
		}
		r.WallMSTotal += row.WallMS
		r.Actions = append(r.Actions, row)
	}
	sort.SliceStable(r.Actions, func(i, j int) bool {
		return r.Actions[i].WallMS > r.Actions[j].WallMS
	})
	return r
}

const consoleTopSlowest = 10

// PrintConsole writes the compact always-on profile section: totals and the
// slowest actions.
func (r *Report) PrintConsole(w io.Writer) {
	fmt.Fprintf(w, "\n⇒ Build profile: %d actions (%d executed), %s wall time\n",
		r.TotalActions, r.ExecutedActions, fmtMS(r.WallMSTotal))

	shown := 0
	for _, row := range r.Actions {
		if shown >= consoleTopSlowest || row.WallMS <= 0 {
			break
		}
		if shown == 0 {
			fmt.Fprintf(w, "   Slowest actions:\n")
		}
		fmt.Fprintf(w, "   %8s  %-9s %s\n",
			fmtMS(row.WallMS), row.Mode, consoleLabel(row))
		shown++
	}
}

// consoleLabel is the row's display name: the package import path, falling
// back to the link target or mode for package-less actions.
func consoleLabel(row Row) string {
	if row.Package != "" {
		return row.Package
	}
	return "(" + row.Mode + ")"
}

// fmtMS renders a millisecond quantity compactly (µs → s range).
func fmtMS(ms float64) string {
	switch {
	case ms >= 10_000:
		return fmt.Sprintf("%.1fs", ms/1000)
	case ms >= 1000:
		return fmt.Sprintf("%.2fs", ms/1000)
	case ms >= 10:
		return fmt.Sprintf("%.0fms", ms)
	default:
		return fmt.Sprintf("%.1fms", ms)
	}
}

// WriteJSON serializes the report to every given path, creating parent
// directories as needed. Failures are reported per path; the earliest error is
// returned after all paths were attempted.
func (r *Report) WriteJSON(paths ...string) error {
	data, err := json.MarshalIndent(r, "", "\t")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	var firstErr error
	for _, path := range paths {
		if mkErr := os.MkdirAll(filepath.Dir(path), 0o755); mkErr != nil && firstErr == nil {
			firstErr = mkErr
			continue
		}
		if wrErr := os.WriteFile(path, data, 0o644); wrErr != nil && firstErr == nil {
			firstErr = wrErr
		}
	}
	return firstErr
}

// AppendStepSummary appends the profile table (totals + top slowest actions)
// to $GITHUB_STEP_SUMMARY, next to the pipeline Gantt the summary writer
// already emits. No-op outside CI (env var unset).
func (r *Report) AppendStepSummary() error {
	path := os.Getenv("GITHUB_STEP_SUMMARY")
	if path == "" {
		return nil
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	var b strings.Builder
	fmt.Fprintf(&b, "\n## Build profile\n\n")
	fmt.Fprintf(&b, "**%d** actions (**%d** executed), **%s** wall time\n\n",
		r.TotalActions, r.ExecutedActions, fmtMS(r.WallMSTotal))
	wrote := 0
	for _, row := range r.Actions {
		if wrote >= consoleTopSlowest || row.WallMS <= 0 {
			break
		}
		if wrote == 0 {
			fmt.Fprintf(&b, "| Wall | Mode | Package |\n|---:|---|---|\n")
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` |\n",
			fmtMS(row.WallMS), row.Mode, consoleLabel(row))
		wrote++
	}
	_, err = f.WriteString(b.String())
	return err
}
