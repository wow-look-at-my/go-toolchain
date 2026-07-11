package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/cache"
)

// testActions builds a small merged graph: a slow miss+put compile, a fast
// cache-satisfied compile, an unknown-outcome root, and a remote hit.
func testActions() []Action {
	t0 := time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
	return []Action{
		{ID: 0, Mode: "go build"}, // no ActionID, never executed
		{ID: 1, Mode: "build", Package: "example.com/m/slow", NeedBuild: true,
			ActionID: "slowslowslowslowslow", TimeStart: t0, TimeDone: t0.Add(2 * time.Second),
			CmdReal: int64(1900 * time.Millisecond)},
		{ID: 2, Mode: "build", Package: "example.com/m/warm",
			ActionID: "warmwarmwarmwarmwarm"},
		{ID: 3, Mode: "link", Package: "example.com/m", NeedBuild: true,
			ActionID: "linklinklinklinklink", TimeStart: t0.Add(2 * time.Second), TimeDone: t0.Add(2500 * time.Millisecond)},
	}
}

func testOutcomes() map[string]cache.ActionOutcome {
	return map[string]cache.ActionOutcome{
		"slowslowslowslowslow": {Get: "miss", Put: true, Bytes: 4096, GetUS: 120, PutUS: 800},
		"warmwarmwarmwarmwarm": {Get: "hit-local", Bytes: 2048, GetUS: 40},
		"linklinklinklinklink": {Get: "hit-remote", Bytes: 1 << 20, GetUS: 90_000},
	}
}

func TestBuildReport_JoinAndTotals(t *testing.T) {
	totals := &CacheTotals{LocalHits: 10, LocalPuts: 2, Misses: 3, Prefetched: 5}
	web := &cache.WebSummary{Hits: 4, Puts: 2, IndexKeys: 1234, IndexAuthoritative: true}
	r := BuildReport(testActions(), testOutcomes(), totals, web, 7)

	assert.Equal(t, ReportSchema, r.Schema)
	assert.Equal(t, 4, r.TotalActions)
	assert.Equal(t, 2, r.ExecutedActions)
	assert.Equal(t, map[string]int{"miss": 1, "hit-local": 1, "hit-remote": 1, "unknown": 1}, r.Outcomes)
	// 2 hits / (2 hits + 1 miss)
	assert.InDelta(t, 66.67, r.SatisfiedPct, 0.1)
	assert.InDelta(t, 2500, r.WallMSTotal, 0.001)
	assert.Equal(t, uint64(7), r.ActionsOverflow)
	assert.Same(t, totals, r.Cache)
	assert.Same(t, web, r.Web)

	// Sorted by wall descending: slow (2000ms), link (500ms), then the rest.
	require.Len(t, r.Actions, 4)
	assert.Equal(t, "example.com/m/slow", r.Actions[0].Package)
	assert.Equal(t, "miss", r.Actions[0].Outcome)
	assert.True(t, r.Actions[0].Put)
	assert.Equal(t, int64(4096), r.Actions[0].Bytes)
	assert.InDelta(t, 2000, r.Actions[0].WallMS, 0.001)
	assert.InDelta(t, 1900, r.Actions[0].CmdRealMS, 0.001)
	assert.NotEmpty(t, r.Actions[0].Start)
	assert.Equal(t, "link", r.Actions[1].Mode)
	assert.Equal(t, "hit-remote", r.Actions[1].Outcome)
}

func TestBuildReport_NoOutcomes(t *testing.T) {
	// A run without GOCACHEPROG (or a vet-only path) joins nothing: every row
	// is unknown and the satisfied pct is 0, not NaN.
	r := BuildReport(testActions(), nil, nil, nil, 0)
	assert.Equal(t, 4, r.Outcomes["unknown"])
	assert.Equal(t, float64(0), r.SatisfiedPct)
	assert.Nil(t, r.Cache)
	assert.Nil(t, r.Web)
}

func TestPrintConsole(t *testing.T) {
	r := BuildReport(testActions(), testOutcomes(), nil, nil, 0)
	var b strings.Builder
	r.PrintConsole(&b)
	out := b.String()

	assert.Contains(t, out, "⇒ Build profile: 4 actions (2 executed), 67% cache-satisfied (hit-local 1  hit-remote 1  miss 1)")
	assert.Contains(t, out, "Slowest actions:")
	assert.Contains(t, out, "example.com/m/slow")
	assert.Contains(t, out, "miss+put")
	assert.Contains(t, out, "hit-remote")
	// Only executed rows are listed as slowest — the warm hit has no wall time.
	assert.NotContains(t, out, "example.com/m/warm")
	assert.Contains(t, out, "Rebuilt despite cache (miss+put):")
	// The rebuilt section aggregates by package.
	assert.Contains(t, out, "2.00s  example.com/m/slow")
}

func TestPrintConsole_NoRebuiltSection(t *testing.T) {
	outcomes := testOutcomes()
	o := outcomes["slowslowslowslowslow"]
	o.Put = false
	outcomes["slowslowslowslowslow"] = o
	r := BuildReport(testActions(), outcomes, nil, nil, 0)

	var b strings.Builder
	r.PrintConsole(&b)
	assert.NotContains(t, b.String(), "Rebuilt despite cache")
}

func TestOutcomeLabelAndFmtMS(t *testing.T) {
	assert.Equal(t, "-", outcomeLabel(Row{}))
	assert.Equal(t, "put", outcomeLabel(Row{Put: true}))
	assert.Equal(t, "miss+put", outcomeLabel(Row{Outcome: "miss", Put: true}))
	assert.Equal(t, "hit-local", outcomeLabel(Row{Outcome: "hit-local"}))

	assert.Equal(t, "0.4ms", fmtMS(0.42))
	assert.Equal(t, "42ms", fmtMS(42.4))
	assert.Equal(t, "1.82s", fmtMS(1820))
	assert.Equal(t, "12.5s", fmtMS(12490))
}

func TestWriteJSON_RoundTripAndSchema(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "build", "profile.json")
	p2 := filepath.Join(dir, "tmpdir", "profile.json")

	r := BuildReport(testActions(), testOutcomes(),
		&CacheTotals{LocalHits: 1}, &cache.WebSummary{Hits: 2, MissChecksum: 1, IndexKeys: 9}, 0)
	require.NoError(t, r.WriteJSON(p1, p2))

	for _, p := range []string{p1, p2} {
		data, err := os.ReadFile(p)
		require.NoError(t, err)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(data, &parsed))
		assert.Equal(t, float64(ReportSchema), parsed["schema"])
		assert.Equal(t, float64(4), parsed["total_actions"])
		web := parsed["web"].(map[string]any)
		assert.Equal(t, float64(1), web["miss_checksum"], "the CI poison tripwire field must serialize under this name")
		assert.Equal(t, float64(9), web["index_keys"])
		cacheBlock := parsed["cache"].(map[string]any)
		assert.Equal(t, float64(1), cacheBlock["local_hits"])
		actions := parsed["actions"].([]any)
		require.Len(t, actions, 4)
	}
}

func TestWriteJSON_ErrorReturned(t *testing.T) {
	r := BuildReport(nil, nil, nil, nil, 0)
	err := r.WriteJSON(filepath.Join(t.TempDir(), "no-such-dir-parent-is-file", "x", "profile.json"))
	// Parent creation succeeds here; instead point at a path whose parent is a file.
	_ = err
	dir := t.TempDir()
	f := filepath.Join(dir, "afile")
	require.NoError(t, os.WriteFile(f, []byte("x"), 0o644))
	assert.Error(t, r.WriteJSON(filepath.Join(f, "profile.json")))
}

func TestAppendStepSummary(t *testing.T) {
	dir := t.TempDir()
	sum := filepath.Join(dir, "summary.md")
	t.Setenv("GITHUB_STEP_SUMMARY", sum)

	r := BuildReport(testActions(), testOutcomes(), nil,
		&cache.WebSummary{Hits: 3, IndexKeys: 42, IndexAuthoritative: true}, 0)
	require.NoError(t, r.AppendStepSummary())

	data, err := os.ReadFile(sum)
	require.NoError(t, err)
	out := string(data)
	assert.Contains(t, out, "## Build profile")
	assert.Contains(t, out, "cache-satisfied")
	assert.Contains(t, out, "| Wall | Mode | Package | Cache |")
	assert.Contains(t, out, "`example.com/m/slow`")
	assert.Contains(t, out, "tripwires — checksum 0, buildid 0, modindex 0")
}

func TestAppendStepSummary_NoEnvIsNoop(t *testing.T) {
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	r := BuildReport(nil, nil, nil, nil, 0)
	assert.NoError(t, r.AppendStepSummary())
}
