package cache

// Regression tests for pack-store append/index ordering: racing Puts for the
// same action must commit index updates in the same order as their appends.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-containers/set"
)

const packRaceRounds = 1500

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

// TestPackStore_ConcurrentSameActionPutRescanConsistency drives rival
// concurrent Puts for the SAME action with DIFFERENT contents and asserts the
// live index and a fresh rescan of the pack files agree on which body the
// action maps to. Before the commit-under-append-lock fix this diverged in
// a small fraction of the racing pairs (live=A, rescan=B). packRaceRounds
// pairs is the sensitivity.
//
// Every pair races into the same store, and the rescan happens at the end. A
// store per pair spends the run on directory churn, which is what an NTFS host
// charges for; a real store holds many actions anyway.
func TestPackStore_ConcurrentSameActionPutRescanConsistency(t *testing.T) {
	t.Parallel()
	rng := rand.New(rand.NewSource(1))
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.NoError(t, err)

	live := make(map[string]string, packRaceRounds)
	for range packRaceRounds {
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
		live[action] = liveLoc.outputID
	}
	require.NoError(t, s.Close())

	s2, err := OpenPackStore(dir)
	require.NoError(t, err)
	defer s2.Close()
	for action, outputID := range live {
		scanLoc, ok := s2.Get(action)
		require.True(t, ok)
		require.Equal(t, outputID, scanLoc.outputID,
			"action %s: live index and pack rescan disagree about its body — the next build would serve different content than this one did", action)
	}
}

// TestPackStore_PutIfAbsent covers the primitive's shapes: filling an absent
// action, refusing to replace a present action, and reusing an existing
// body via an alias record — with the result surviving a rescan.
func TestPackStore_PutIfAbsent(t *testing.T) {
	t.Parallel()
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

	// Alias shape: another action for content that is already stored.
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
	t.Parallel()
	rng := rand.New(rand.NewSource(3))
	dir := t.TempDir()
	s, err := OpenPackStore(dir)
	require.NoError(t, err)

	want := make(map[string]string, packRaceRounds)
	for range packRaceRounds {
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
			"action %s: a prefetched body displaced the locally-stored one in the live index", action)
		want[action] = outPut
	}
	require.NoError(t, s.Close())

	s2, err := OpenPackStore(dir)
	require.NoError(t, err)
	defer s2.Close()
	for action, outputID := range want {
		scanLoc, ok := s2.Get(action)
		require.True(t, ok)
		require.Equal(t, outputID, scanLoc.outputID,
			"action %s: a prefetched body displaced the locally-stored one in the pack replay order", action)
	}
}

// TestLocalCache_PutIfAbsent pins the loose tier's variant: absent fills,
// present is never replaced.
func TestLocalCache_PutIfAbsent(t *testing.T) {
	t.Parallel()
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
// sticky forever while silently dropping the freshly computed correct body.
func TestServer_PutReplacesMismatchedOutputID(t *testing.T) {
	t.Parallel()
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

// forgetRecorder wraps an IBackend and records ForgetStale calls.
type forgetRecorder struct {
	IBackend
	mu     sync.Mutex
	forgot []string
}

func (f *forgetRecorder) ForgetStale(actionID string) {
	f.mu.Lock()
	f.forgot = append(f.forgot, actionID)
	f.mu.Unlock()
}

// TestServer_PutReplaceForgetsStaleWebClaim: when the PUT replace path
// overwrites a mismatched local entry, it must also drop the remote's
// optimistic claim for that key (via staleKeyForgetter), so the immediately
// following remote Put uploads the fresh body instead of skipping it as
// already-present — that upload is what heals the shared tier.
func TestServer_PutReplaceForgetsStaleWebClaim(t *testing.T) {
	t.Parallel()
	lc, err := NewLocalCache(t.TempDir())
	require.NoError(t, err)
	remote := &forgetRecorder{IBackend: newMemBackend()}
	srv := NewServer(lc, remote)

	actionID := bytes.Repeat([]byte{0xd2}, 32)
	stale := "stale body"
	fresh := "fresh replacement body"
	staleSum := sha256.Sum256([]byte(stale))
	freshSum := sha256.Sum256([]byte(fresh))

	var input strings.Builder
	input.WriteString(makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID, OutputID: staleSum[:], BodySize: int64(len(stale)),
	}, stale))
	input.WriteString(makePutRequest(Request{
		ID: 2, Command: CmdPut, ActionID: actionID, OutputID: freshSum[:], BodySize: int64(len(fresh)),
	}, fresh))
	input.WriteString(makePutRequest(Request{ // exact re-put: dedup, no forget
		ID: 3, Command: CmdPut, ActionID: actionID, OutputID: freshSum[:], BodySize: int64(len(fresh)),
	}, fresh))
	input.WriteString(makeRequest(Request{ID: 4, Command: CmdClose}))

	var out bytes.Buffer
	require.NoError(t, srv.Run(strings.NewReader(input.String()), &out))

	remote.mu.Lock()
	defer remote.mu.Unlock()
	require.Equal(t, []string{hex.EncodeToString(actionID)}, remote.forgot,
		"exactly the replace (mismatch) PUT must drop the stale remote claim; matching dedup PUTs must not")
}

// TestWebBackend_ForgetStaleDropsClaim: ForgetStale removes the optimistic
// index claim so a later Put's check-and-claim re-uploads, and the
// noCloseBackend daemon wrapper forwards the capability.
func TestWebBackend_ForgetStaleDropsClaim(t *testing.T) {
	t.Parallel()
	b := &WebBackend{prefix: "go-buildcache/"}
	b.keys = set.New[string]()
	action := strings.Repeat("d", 64)
	key := b.key(action)
	b.keys.Add(key)
	require.True(t, b.keyKnown(key))

	var viaWrapper staleKeyForgetter = &noCloseBackend{b}
	viaWrapper.ForgetStale(action)
	require.False(t, b.keyKnown(key),
		"ForgetStale (through the daemon wrapper) must drop the stale index claim")
}

// TestWireBatchCallbacks_PrefetchNeverReplacesLocalEntry: a prefetched entry
// for an action the local tier already holds must be dropped, and must not be
// counted as populated.
func TestWireBatchCallbacks_PrefetchNeverReplacesLocalEntry(t *testing.T) {
	t.Parallel()
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
