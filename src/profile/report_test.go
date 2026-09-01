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
)

// testActions builds a small merged graph: an executed compile, an
// unexecuted (cached, no wall time) compile, an unexecuted root, and an
// executed link.
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

func TestBuildReport_Totals(t *testing.T) {
	r := BuildReport(testActions())

	assert.Equal(t, ReportSchema, r.Schema)
	assert.Equal(t, 4, r.TotalActions)
	assert.Equal(t, 2, r.ExecutedActions)
	assert.InDelta(t, 2500, r.WallMSTotal, 0.001)

	// Sorted by wall descending: slow (2000ms), link (500ms), then the rest.
	require.Len(t, r.Actions, 4)
	assert.Equal(t, "example.com/m/slow", r.Actions[0].Package)
	assert.InDelta(t, 2000, r.Actions[0].WallMS, 0.001)
	assert.InDelta(t, 1900, r.Actions[0].CmdRealMS, 0.001)
	assert.NotEmpty(t, r.Actions[0].Start)
	assert.Equal(t, "link", r.Actions[1].Mode)
}

func TestPrintConsole(t *testing.T) {
	r := BuildReport(testActions())
	var b strings.Builder
	r.PrintConsole(&b)
	out := b.String()

	assert.Contains(t, out, "⇒ Build profile: 4 actions (2 executed), 2.50s wall time")
	assert.Contains(t, out, "Slowest actions:")
	assert.Contains(t, out, "example.com/m/slow")
	// Only executed rows are listed as slowest — the warm hit has no wall time.
	assert.NotContains(t, out, "example.com/m/warm")
}

func TestFmtMS(t *testing.T) {
	assert.Equal(t, "0.4ms", fmtMS(0.42))
	assert.Equal(t, "42ms", fmtMS(42.4))
	assert.Equal(t, "1.82s", fmtMS(1820))
	assert.Equal(t, "12.5s", fmtMS(12490))
}

func TestWriteJSON_RoundTripAndSchema(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "build", "profile.json")
	p2 := filepath.Join(dir, "tmpdir", "profile.json")

	r := BuildReport(testActions())
	require.NoError(t, r.WriteJSON(p1, p2))

	for _, p := range []string{p1, p2} {
		data, err := os.ReadFile(p)
		require.NoError(t, err)
		var parsed map[string]any
		require.NoError(t, json.Unmarshal(data, &parsed))
		assert.Equal(t, float64(ReportSchema), parsed["schema"])
		assert.Equal(t, float64(4), parsed["total_actions"])
		actions := parsed["actions"].([]any)
		require.Len(t, actions, 4)
	}
}

func TestWriteJSON_ErrorReturned(t *testing.T) {
	r := BuildReport(nil)
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

	r := BuildReport(testActions())
	require.NoError(t, r.AppendStepSummary())

	data, err := os.ReadFile(sum)
	require.NoError(t, err)
	out := string(data)
	assert.Contains(t, out, "## Build profile")
	assert.Contains(t, out, "wall time")
	assert.Contains(t, out, "| Wall | Mode | Package |")
	assert.Contains(t, out, "`example.com/m/slow`")
}

func TestAppendStepSummary_NoEnvIsNoop(t *testing.T) {
	t.Setenv("GITHUB_STEP_SUMMARY", "")
	r := BuildReport(nil)
	assert.NoError(t, r.AppendStepSummary())
}
