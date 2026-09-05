package profile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// graphJSON is a realistic -debug-actiongraph dump fragment (field shapes
// match cmd/go: RFC3339 times, ns durations, unknown fields present).
const graphJSON = `[
	{"ID":0,"Mode":"go build","Priority":3,"Deps":[1],"UnknownField":true},
	{"ID":1,"Mode":"build","Package":"example.com/m/pkga","NeedBuild":true,
	 "ActionID":"aaaaaaaaaaaaaaaaaaaa","BuildID":"x/y",
	 "TimeReady":"2026-07-04T10:00:00Z","TimeStart":"2026-07-04T10:00:01Z","TimeDone":"2026-07-04T10:00:03Z",
	 "CmdReal":1500000000,"CmdUser":1200000000,"CmdSys":100000000},
	{"ID":2,"Mode":"build","Package":"example.com/m/pkgb",
	 "ActionID":"bbbbbbbbbbbbbbbbbbbb"}
]`

func writeGraph(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return path
}

func TestLoadGraphs_ParsesAndMerges(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	g1 := writeGraph(t, dir, "g1.json", graphJSON)
	// The later dump: pkga reappears with the SAME ActionID but unexecuted (cache-satisfied); the
	// executed instance from g1 must win the merge.
	g2 := writeGraph(t, dir, "g2.json", `[
		{"ID":1,"Mode":"build","Package":"example.com/m/pkga","ActionID":"aaaaaaaaaaaaaaaaaaaa"}
	]`)

	var warn bytes.Buffer
	actions := LoadGraphs([]string{g1, g2}, &warn)
	require.Empty(t, warn.String())

	// The rows: the ActionID-less root, pkga (merged), pkgb.
	require.Len(t, actions, 3)
	var pkga *Action
	for i := range actions {
		if actions[i].Package == "example.com/m/pkga" {
			pkga = &actions[i]
		}
	}
	require.NotNil(t, pkga)
	assert.True(t, pkga.Executed(), "the executed instance must win the merge")
	assert.Equal(t, 2*time.Second, pkga.Wall())
	assert.Equal(t, int64(1500000000), pkga.CmdReal)
}

func TestLoadGraphs_MergePrefersLongerExecution(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	g := writeGraph(t, dir, "g.json", `[
		{"ID":1,"Mode":"build","Package":"p","ActionID":"cccccccccccccccccccc",
		 "TimeStart":"2026-07-04T10:00:00Z","TimeDone":"2026-07-04T10:00:01Z"},
		{"ID":1,"Mode":"build","Package":"p","ActionID":"cccccccccccccccccccc",
		 "TimeStart":"2026-07-04T11:00:00Z","TimeDone":"2026-07-04T11:00:05Z"}
	]`)
	actions := LoadGraphs([]string{g}, nil)
	require.Len(t, actions, 1)
	assert.Equal(t, 5*time.Second, actions[0].Wall())
}

func TestLoadGraphs_MissingFileIsSilent(t *testing.T) {
	t.Serial()
	var warn bytes.Buffer
	actions := LoadGraphs([]string{filepath.Join(t.TempDir(), "never-written.json")}, &warn)
	assert.Empty(t, actions)
	assert.Empty(t, warn.String(), "a go invocation that failed before dumping must not warn")
}

func TestLoadGraphs_MalformedFileWarnsAndSkips(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	bad := writeGraph(t, dir, "bad.json", "{not json[")
	good := writeGraph(t, dir, "good.json", graphJSON)

	var warn bytes.Buffer
	actions := LoadGraphs([]string{bad, good}, &warn)
	assert.Len(t, actions, 3, "the good graph must still load")
	assert.Contains(t, warn.String(), "bad.json")
	assert.Contains(t, warn.String(), "Warning: build profile")
}

func TestActionExecutedAndWall(t *testing.T) {
	t.Serial()
	var a Action
	assert.False(t, a.Executed())
	assert.Equal(t, time.Duration(0), a.Wall())

	a.TimeStart = time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)
	assert.False(t, a.Executed(), "start without done is not executed")

	a.TimeDone = a.TimeStart.Add(-time.Second)
	assert.False(t, a.Executed(), "done before start is not executed")

	a.TimeDone = a.TimeStart.Add(300 * time.Millisecond)
	assert.True(t, a.Executed())
	assert.Equal(t, 300*time.Millisecond, a.Wall())
}
