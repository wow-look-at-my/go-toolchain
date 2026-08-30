package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOutputIDMatches(t *testing.T) {
	t.Parallel()
	body := []byte("the quick brown fox jumps over the lazy dog")
	sum := sha256.Sum256(body)
	want := hex.EncodeToString(sum[:])

	got, ok := outputIDMatches(want, body)
	require.True(t, ok)
	require.Equal(t, want, got)

	// Case-insensitive: an upper-cased id still matches.
	_, ok = outputIDMatches(strings.ToUpper(want), body)
	require.True(t, ok)

	// A wrong id, a truncated body, and an empty id are all rejected.
	_, ok = outputIDMatches("deadbeef", body)
	require.False(t, ok)
	_, ok = outputIDMatches(want, body[:len(body)-1])
	require.False(t, ok)
	_, ok = outputIDMatches("", body)
	require.False(t, ok)

	// The empty body has a well-defined hash and verifies against it.
	emptySum := sha256.Sum256(nil)
	_, ok = outputIDMatches(hex.EncodeToString(emptySum[:]), nil)
	require.True(t, ok)
}

// noopSink is a statsSink that discards prefetch-population counts.
type noopSink struct{}

func (noopSink) recordBatchPop(uint32) {}

// TestWireBatchCallbacks_SkipsCorruptPrefetch verifies the prefetch population
// path never writes a body that fails the outputID checksum into the local
// pack: a good entry is populated, a corrupt entry (body that does not hash to its
// advertised outputID) is skipped, so it can never be served as a "valid" local
// hit and fail the build with "corrupt index".
func TestWireBatchCallbacks_SkipsCorruptPrefetch(t *testing.T) {
	t.Parallel()
	local, err := NewLocalCache(t.TempDir())
	require.NoError(t, err)
	defer local.Close()

	wb := &WebBackend{prefix: "go-buildcache/"}
	wireBatchCallbacks(wb, local, noopSink{})

	good := "good prefetch body"
	goodCompressed, _ := compressData([]byte(good))
	// The corrupt entry advertises the good body's hash but ships different bytes.
	corruptCompressed, _ := compressData([]byte("a totally different body"))

	aidGood := strings.Repeat("a", 64)
	aidBad := strings.Repeat("b", 64)
	wb.OnBatchEntries([]BatchEntry{
		{Key: "go-buildcache/v1" + aidGood, OutputID: testOutputID(good), Data: goodCompressed, Prefetch: true},
		{Key: "go-buildcache/v1" + aidBad, OutputID: testOutputID(good), Data: corruptCompressed, Prefetch: true},
	})

	_, missGood := local.Get(aidGood)
	require.False(t, missGood, "a valid prefetch entry must populate the local cache")
	_, missBad := local.Get(aidBad)
	require.True(t, missBad, "a corrupt prefetch entry must be skipped, not populated")
}
