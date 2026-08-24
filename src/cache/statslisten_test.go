package cache

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTruncateActionID(t *testing.T) {
	// Golden pair from the build-id canary; the truncated form must equal cmd/go's actiongraph ActionID rendering.
	raw := hexToBytes("10f94fc02dcc245820dd861f4c6c25dee23ceb750f6be498fe84f67dfd2f1f9b")
	assert.Equal(t, "EPlPwC3MJFgg3YYfTGwl", truncateActionID(raw))

	assert.Equal(t, "", truncateActionID(nil))
	assert.Equal(t, "", truncateActionID(raw[:14]), "shorter than 15 bytes: no truncated form")
	assert.NotEqual(t, "", truncateActionID(raw[:15]))
}

func TestWithAction(t *testing.T) {
	id := bytes.Repeat([]byte{0xab}, 16)
	ev := withAction(StatEvent{LocalHit: 1}, id, "get", "hit-local", 123, 4*time.Millisecond)
	assert.Equal(t, uint32(1), ev.LocalHit)
	assert.Equal(t, truncateActionID(id), ev.Action)
	assert.Equal(t, "get", ev.Op)
	assert.Equal(t, "hit-local", ev.Outcome)
	assert.Equal(t, int64(123), ev.Bytes)
	assert.Equal(t, int64(4000), ev.DurUS)

	// Too-short ID: the counter event goes out unchanged, no action fields.
	ev = withAction(StatEvent{Miss: 1}, []byte{1, 2, 3}, "get", "miss", 0, time.Millisecond)
	assert.Equal(t, uint32(1), ev.Miss)
	assert.Equal(t, "", ev.Action)
	assert.Equal(t, "", ev.Op)
}

func TestRecordAction_MergePolicy(t *testing.T) {
	sl := &StatsListener{}

	// First get outcome wins; a later warm re-get must not overwrite it.
	sl.recordAction(&StatEvent{Action: "a", Op: "get", Outcome: "miss", DurUS: 50})
	sl.recordAction(&StatEvent{Action: "a", Op: "put", Outcome: "put", Bytes: 2048, DurUS: 90})
	sl.recordAction(&StatEvent{Action: "a", Op: "get", Outcome: "hit-local", Bytes: 2048, DurUS: 10})

	got := sl.Actions()
	require.Len(t, got, 1)
	ao := got["a"]
	assert.Equal(t, "miss", ao.Get, "the first get outcome is the one that cost the rebuild")
	assert.Equal(t, int64(50), ao.GetUS)
	assert.True(t, ao.Put)
	assert.Equal(t, int64(90), ao.PutUS)
	assert.Equal(t, int64(2048), ao.Bytes)
	assert.Equal(t, uint64(0), sl.ActionsOverflow())
}

func TestRecordAction_OverflowCap(t *testing.T) {
	sl := &StatsListener{}
	for i := 0; i < maxTrackedActions; i++ {
		sl.recordAction(&StatEvent{Action: fmt.Sprintf("a%06d", i), Op: "get", Outcome: "miss"})
	}
	// Past the cap: new IDs are dropped and counted, existing IDs still update.
	sl.recordAction(&StatEvent{Action: "overflow-1", Op: "get", Outcome: "miss"})
	sl.recordAction(&StatEvent{Action: "overflow-2", Op: "put", Outcome: "put"})
	sl.recordAction(&StatEvent{Action: "a000000", Op: "put", Outcome: "put"})

	assert.Equal(t, uint64(2), sl.ActionsOverflow())
	got := sl.Actions()
	assert.Len(t, got, maxTrackedActions)
	assert.True(t, got["a000000"].Put, "existing entries keep aggregating past the cap")
	_, tracked := got["overflow-1"]
	assert.False(t, tracked)
}

// TestStatsStreaming_PerAction is the end-to-end pipe: a real Server run
// (PUT, warm GET, missing GET) must deliver per-action outcome events over
// the stats socket, keyed by the truncated action ID.
func TestStatsStreaming_PerAction(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "stats.sock")

	sl, err := NewStatsListener(sockPath)
	require.NoError(t, err)

	t.Setenv("GOCACHE_STATS_SOCK", sockPath)

	actionID := bytes.Repeat([]byte{0xab, 0xcd}, 16) // 32 bytes, like a real wire ID
	missID := bytes.Repeat([]byte{0xff, 0x00}, 16)
	sum := sha256.Sum256([]byte("hello"))

	lc, err := NewLocalCache(filepath.Join(dir, "cache"))
	require.NoError(t, err)

	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: sum[:], BodySize: 5,
	}, "hello"))
	input.WriteString(makeRequest(Request{ID: 2, Command: CmdGet, ActionID: actionID}))
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdGet, ActionID: missID}))
	input.WriteString(makeRequest(Request{ID: 4, Command: CmdClose}))

	var out bytes.Buffer
	srv := NewServer(lc, nil)
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	sl.Close()
	got := sl.Actions()
	require.Len(t, got, 2)

	stored := got[truncateActionID(actionID)]
	assert.True(t, stored.Put)
	assert.Equal(t, "hit-local", stored.Get, "the warm re-get after the put is a local hit")
	assert.Equal(t, int64(5), stored.Bytes)
	assert.Greater(t, stored.PutUS, int64(0))
	assert.Greater(t, stored.GetUS, int64(0))

	missed := got[truncateActionID(missID)]
	assert.Equal(t, "miss", missed.Get)
	assert.False(t, missed.Put)
	assert.Equal(t, uint64(0), sl.ActionsOverflow())
}
