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

	"github.com/wow-look-at-my/go-toolchain/src/cache"
)

// ReportSchema versions the profile.json layout. Bump on incompatible change.
const ReportSchema = 1

// Row is an action of the profile: an actiongraph row joined with the cache
// outcome observed for its ActionID.
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
	Outcome   string  `json:"outcome,omitempty"` // hit-local | hit-remote | miss ("" = no cache get observed)
	Put       bool    `json:"put,omitempty"`     // output stored into the cache this run
	Bytes     int64   `json:"bytes,omitempty"`   // cached object size
	GetUS     int64   `json:"get_us,omitempty"`  // cache get duration, µs
	PutUS     int64   `json:"put_us,omitempty"`  // cache put duration, µs
}

// CacheTotals mirrors the end-of-build cache stat line (the StatsListener
// aggregate across every go subprocess of the run).
type CacheTotals struct {
	LocalHits  uint32 `json:"local_hits"`
	LocalPuts  uint32 `json:"local_puts"`
	RemoteHits uint32 `json:"remote_hits"`
	RemotePuts uint32 `json:"remote_puts"`
	Misses     uint32 `json:"misses"`
	Prefetched uint32 `json:"prefetched"`
}

// Report is the build profile serialized to build/profile.json and
// $TMPDIR/go-toolchain-profile/profile.json. Stable schema (versioned by the
// Schema field): Outcomes tallies get outcomes; SatisfiedPct is
// the hit share of rows with a known outcome, as a percentage; WallMSTotal sums
// per-action wall time without folding out parallelism; Web is absent with
// no web backend configured; Actions is the full join, by wall time desc.
type Report struct {
	Schema          int               `json:"schema"`
	Created         time.Time         `json:"created"`
	TotalActions    int               `json:"total_actions"`
	ExecutedActions int               `json:"executed_actions"`
	Outcomes        map[string]int    `json:"cache_outcomes"`
	SatisfiedPct    float64           `json:"cache_satisfied_pct"`
	WallMSTotal     float64           `json:"wall_ms_total"`
	ActionsOverflow uint64            `json:"actions_overflow,omitempty"`
	Cache           *CacheTotals      `json:"cache,omitempty"`
	Web             *cache.WebSummary `json:"web,omitempty"`
	Actions         []Row             `json:"actions"`
}

// BuildReport joins the merged actiongraph with the per-action cache
// outcomes and attaches the run-wide counters.
func BuildReport(actions []Action, outcomes map[string]cache.ActionOutcome, totals *CacheTotals, web *cache.WebSummary, overflow uint64) *Report {
	r := &Report{
		Schema:          ReportSchema,
		Created:         time.Now().UTC(),
		TotalActions:    len(actions),
		Outcomes:        map[string]int{},
		ActionsOverflow: overflow,
		Cache:           totals,
		Web:             web,
		Actions:         make([]Row, 0, len(actions)),
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
		if ao, ok := outcomes[a.ActionID]; ok && a.ActionID != "" {
			row.Outcome = ao.Get
			row.Put = ao.Put
			row.Bytes = ao.Bytes
			row.GetUS = ao.GetUS
			row.PutUS = ao.PutUS
		}
		key := row.Outcome
		if key == "" {
			key = "unknown"
		}
		r.Outcomes[key]++
		r.WallMSTotal += row.WallMS
		r.Actions = append(r.Actions, row)
	}
	hits := r.Outcomes["hit-local"] + r.Outcomes["hit-remote"]
	if known := hits + r.Outcomes["miss"]; known > 0 {
		r.SatisfiedPct = float64(hits) * 100 / float64(known)
	}
	sort.SliceStable(r.Actions, func(i, j int) bool {
		return r.Actions[i].WallMS > r.Actions[j].WallMS
	})
	return r
}

const (
	consoleTopSlowest = 10
	consoleTopRebuilt = 5
)

// PrintConsole writes the compact always-on profile section: totals, the
// slowest actions, and the packages that were rebuilt despite the cache
// (miss + put this run — on a warm build these are the cache defeats worth
// investigating).
func (r *Report) PrintConsole(w io.Writer) {
	fmt.Fprintf(w, "\n⇒ Build profile: %d actions (%d executed), %.0f%% cache-satisfied (hit-local %d  hit-remote %d  miss %d)\n",
		r.TotalActions, r.ExecutedActions, r.SatisfiedPct,
		r.Outcomes["hit-local"], r.Outcomes["hit-remote"], r.Outcomes["miss"])

	shown := 0
	for _, row := range r.Actions {
		if shown >= consoleTopSlowest || row.WallMS <= 0 {
			break
		}
		if shown == 0 {
			fmt.Fprintf(w, "   Slowest actions:\n")
		}
		fmt.Fprintf(w, "   %8s  %-9s %-52s %s\n",
			fmtMS(row.WallMS), row.Mode, consoleLabel(row), outcomeLabel(row))
		shown++
	}

	type rebuilt struct {
		pkg    string
		wallMS float64
		n      int
	}
	byPkg := map[string]*rebuilt{}
	for _, row := range r.Actions {
		if row.Outcome != "miss" || !row.Put || row.Package == "" {
			continue
		}
		rb := byPkg[row.Package]
		if rb == nil {
			rb = &rebuilt{pkg: row.Package}
			byPkg[row.Package] = rb
		}
		rb.wallMS += row.WallMS
		rb.n++
	}
	if len(byPkg) == 0 {
		return
	}
	list := make([]*rebuilt, 0, len(byPkg))
	for _, rb := range byPkg {
		list = append(list, rb)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].wallMS > list[j].wallMS })
	if len(list) > consoleTopRebuilt {
		list = list[:consoleTopRebuilt]
	}
	fmt.Fprintf(w, "   Rebuilt despite cache (miss+put):\n")
	for _, rb := range list {
		fmt.Fprintf(w, "   %8s  %s\n", fmtMS(rb.wallMS), rb.pkg)
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

// outcomeLabel renders a row's cache outcome, e.g. "hit-local", "miss+put",
// or "-" when no cache activity was observed.
func outcomeLabel(row Row) string {
	label := row.Outcome
	if row.Put {
		if label == "" {
			label = "put"
		} else {
			label += "+put"
		}
	}
	if label == "" {
		return "-"
	}
	return label
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

// AppendStepSummary appends the profile table (cache totals + top slowest
// actions) to $GITHUB_STEP_SUMMARY, next to the pipeline Gantt the summary
// writer already emits. No-op outside CI (env var unset).
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
	fmt.Fprintf(&b, "**%d** actions (**%d** executed), **%.0f%%** cache-satisfied — hit-local %d, hit-remote %d, miss %d, unknown %d\n\n",
		r.TotalActions, r.ExecutedActions, r.SatisfiedPct,
		r.Outcomes["hit-local"], r.Outcomes["hit-remote"], r.Outcomes["miss"], r.Outcomes["unknown"])
	if r.Web != nil {
		fmt.Fprintf(&b, "Web tier: %d hits, %d puts, %d index keys (authoritative: %v); tripwires — checksum %d, buildid %d, modindex %d\n\n",
			r.Web.Hits, r.Web.Puts, r.Web.IndexKeys, r.Web.IndexAuthoritative,
			r.Web.MissChecksum, r.Web.MissBuildID, r.Web.MissModuleIndex)
	}
	wrote := 0
	for _, row := range r.Actions {
		if wrote >= consoleTopSlowest || row.WallMS <= 0 {
			break
		}
		if wrote == 0 {
			fmt.Fprintf(&b, "| Wall | Mode | Package | Cache |\n|---:|---|---|---|\n")
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` | %s |\n",
			fmtMS(row.WallMS), row.Mode, consoleLabel(row), outcomeLabel(row))
		wrote++
	}
	_, err = f.WriteString(b.String())
	return err
}
