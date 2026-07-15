package cache

// Regression tests for the pack-store append/index ordering guarantees and
// the atomic PutIfAbsent primitive.
//
// Background (CI run 29410024636, "golang.org/x/exp/slices: corrupt index" on
// the warm second build): PackStore.Put used to append the record under the
// write lock but update the in-memory index in a separate critical section.
// Two racing Puts for the same action with different contents could therefore
// commit their index updates in the OPPOSITE order of their file appends. The
// live daemon then served one body while the next process's startup scan
// ("last write wins" over file order) served the other — the poisoned mapping
// surfaced only on the NEXT build, as an unrecoverable "corrupt index". The
// racing writer pair in production is a cmd/go PUT vs the web prefetch
// population (wireBatchCallbacks), which runs on the batch coalescer's
// goroutine with no per-action serialization.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// mkPackBody returns (body, outputID-hex) for deterministic pseudo-random
// content of the given size.
func mkPackBody(rng *rand.Rand, size int) ([]byte, string) {
	body := make([]byte, size)
	rng.Read(body)
	sum := sha256.Sum256(body)
	return body, hex.EncodeToString(sum[:])
}

func mkActionID(rng *rand.Rand) string {
	var a [32]byte
	rng.Read(a[:])
	return hex.EncodeToString(a[:])
}

// TestPackStore_ConcurrentSameActionPutRescanConsistency drives two
// concurrent Puts for the SAME action with DIFFERENT contents and asserts the
// live index and a fresh rescan of the pack files agree on which body the
// action maps to. Before the commit-under-append-lock fix this diverged in
// roughly 1 in 1500 iterations (live=A, rescan=B).
func TestPackStore_ConcurrentSameActionPutRescanConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const iters = 1500
	for it := 0; it < iters; it++ {
		dir := t.TempDir()
		s, err := OpenPackStore(dir)
		require.NoError(t, err)

		action := mkActionID(rng)
		bodyA, outA := mkPackBody(rng, 128)
		bodyB, outB := mkPackBody(rng, 256)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := s.Put(action, outA, bytes.NewReader(bodyA))
			require.NoError(t, err)
		}()
		go func() {
			defer wg.Done()
			_, err := s.Put(action, outB, bytes.NewReader(bodyB))
			require.NoError(t, err)
		}()
		wg.Wait()

		liveLoc, ok := s.Get(action)
		require.True(t, ok)
		require.NoError(t, s.Close())

		s2, err := OpenPackStore(dir)
		require.NoError(t, err)
		scanLoc, ok := s2.Get(action)
		require.True(t, ok)
		require.Equal(t, liveLoc.outputID, scanLoc.outputID,
			"iter %d: live index and pack rescan disagree about the body for the action — the next build would serve different content than this one did", it)
		require.NoError(t, s2.Close())
	}
}

// TestPackStore_PutIfAbsent covers the primitive's three shapes: filling an
// absent action, refusing to replace a present one, and reusing an existing
// body via an alias record — with the result surviving a rescan.
func TestPackStore_PutIfAbsent(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.NoError(t, err)

	action := mkActionID(rng)
	bodyA, outA := mkPackBody(rng, 100)
	bodyB, outB := mkPackBody(rng, 100)

	// Absent: stores.
	loc, stored, err := s.PutIfAbsent(action, outA, bytes.NewReader(bodyA))
	require.NoError(t, err)
	require.True(t, stored)
	require.Equal(t, outA, loc.outputID)

	// Present: refuses to replace, returns the existing mapping.
	loc, stored, err = s.PutIfAbsent(action, outB, bytes.NewReader(bodyB))
	require.NoError(t, err)
	require.False(t, stored)
	require.Equal(t, outA, loc.outputID, "PutIfAbsent must never displace an existing entry")

	// Alias shape: a second action for content that is already stored.
	action2 := mkActionID(rng)
	loc, stored, err = s.PutIfAbsent(action2, outA, bytes.NewReader(bodyA))
	require.NoError(t, err)
	require.True(t, stored)
	require.Equal(t, outA, loc.outputID)

	require.NoError(t, s.Close())

	// The refusal and the alias both survive a rescan.
	s2, err := OpenPackStore(dir)
	require.NoError(t, err)
	defer s2.Close()
	got, ok := s2.Get(action)
	require.True(t, ok)
	require.Equal(t, outA, got.outputID)
	got, ok = s2.Get(action2)
	require.True(t, ok)
	require.Equal(t, outA, got.outputID)
}

// TestPackStore_PutAlwaysBeatsPutIfAbsent races a Put (the local cmd/go
// storing its computed body) against a PutIfAbsent (a web prefetch) for the
// same action and asserts the Put's content wins every time — live and after
// a rescan. Whichever way the race resolves, PutIfAbsent either fills a truly
// absent slot (and the later Put overwrites it) or aborts against the
// existing entry; it can never end up shadowing the Put.
func TestPackStore_PutAlwaysBeatsPutIfAbsent(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	const iters = 1500
	for it := 0; it < iters; it++ {
		dir := t.TempDir()
		s, err := OpenPackStore(dir)
		require.NoError(t, err)

		action := mkActionID(rng)
		bodyPut, outPut := mkPackBody(rng, 96)
		bodyPre, outPre := mkPackBody(rng, 160)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := s.Put(action, outPut, bytes.NewReader(bodyPut))
			require.NoError(t, err)
		}()
		go func() {
			defer wg.Done()
			_, _, err := s.PutIfAbsent(action, outPre, bytes.NewReader(bodyPre))
			require.NoError(t, err)
		}()
		wg.Wait()

		liveLoc, ok := s.Get(action)
		require.True(t, ok)
		require.Equal(t, outPut, liveLoc.outputID,
			"iter %d: a prefetched body displaced the locally-stored one in the live index", it)
		require.NoError(t, s.Close())

		s2, err := OpenPackStore(dir)
		require.NoError(t, err)
		scanLoc, ok := s2.Get(action)
		require.True(t, ok)
		require.Equal(t, outPut, scanLoc.outputID,
			"iter %d: a prefetched body displaced the locally-stored one in the pack replay order", it)
		require.NoError(t, s2.Close())
	}
}

// TestLocalCache_PutIfAbsent pins the loose tier's variant: absent fills,
// present is never replaced.
func TestLocalCache_PutIfAbsent(t *testing.T) {
	c, err := NewLocalCache(t.TempDir())
	require.NoError(t, err)
	defer c.Close()

	action := strings.Repeat("ab", 32)
	bodyA := "loose body A"
	bodyB := "loose body BB"

	stored, err := c.PutIfAbsent(action, testOutputID(bodyA), strings.NewReader(bodyA))
	require.NoError(t, err)
	require.True(t, stored)

	stored, err = c.PutIfAbsent(action, testOutputID(bodyB), strings.NewReader(bodyB))
	require.NoError(t, err)
	require.False(t, stored, "PutIfAbsent must never displace an existing entry")

	meta, miss := c.Peek(action)
	require.False(t, miss)
	require.Equal(t, testOutputID(bodyA), meta.OutputID)
	require.Equal(t, int64(len(bodyA)), meta.Size)
}

// TestServer_PutReplacesMismatchedOutputID: a PUT whose outputID differs from
// the stored entry's must overwrite it, not be dedup-discarded. cmd/go is the
// source of truth for its own action keys; the old unconditional dedup made a
// mis-keyed body (e.g. a web-prefetched object under a module-index key)
// sticky forever while silently dropping the freshly computed correct one.
func TestServer_PutReplacesMismatchedOutputID(t *testing.T) {
	lc, err := NewLocalCache(t.TempDir())
	require.NoError(t, err)
	srv := NewServer(lc, nil)

	actionID := bytes.Repeat([]byte{0xd1}, 32)
	stale := "stale mis-keyed body"
	fresh := "freshly computed body"
	staleSum := sha256.Sum256([]byte(stale))
	freshSum := sha256.Sum256([]byte(fresh))

	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: staleSum[:], BodySize: int64(len(stale)),
	}, stale))
	input.WriteString(makePutRequest(Request{ // different content, same action: must replace
		ID: 2, Command: CmdPut, ActionID: actionID, OutputID: freshSum[:], BodySize: int64(len(fresh)),
	}, fresh))
	input.WriteString(makeRequest(Request{ID: 3, Command: CmdClose}))

	var out bytes.Buffer
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	meta, miss := lc.Peek(hex.EncodeToString(actionID))
	require.False(t, miss)
	require.Equal(t, hex.EncodeToString(freshSum[:]), meta.OutputID,
		"a re-PUT with different content must overwrite the stored entry")
	require.Equal(t, uint32(2), lc.Stats.Puts.Load())
}

// TestWireBatchCallbacks_PrefetchNeverReplacesLocalEntry: a prefetched entry
// for an action the local tier already holds must be dropped, and must not be
// counted as populated.
func TestWireBatchCallbacks_PrefetchNeverReplacesLocalEntry(t *testing.T) {
	local, err := NewLocalCache(t.TempDir())
	require.NoError(t, err)
	defer local.Close()

	wb := &WebBackend{prefix: "go-buildcache/"}
	wireBatchCallbacks(wb, local, noopSink{})

	action := strings.Repeat("c", 64)
	localBody := "locally computed body"
	_, err = local.Put(action, testOutputID(localBody), strings.NewReader(localBody))
	require.NoError(t, err)

	webBody := "web-originated body"
	webCompressed, _ := compressData([]byte(webBody))
	wb.OnBatchEntries([]BatchEntry{
		{Key: "go-buildcache/v1" + action, OutputID: testOutputID(webBody), Data: webCompressed, Prefetch: true},
	})

	meta, miss := local.Peek(action)
	require.False(t, miss)
	require.Equal(t, testOutputID(localBody), meta.OutputID,
		"a prefetched body must never displace a locally-originated entry")
}
