package cache

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCanonicalKeyNamespace(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" means: expect the hex hash of the input
	}{
		{"empty stays empty", "", ""},
		{"canonical 16-hex passes through", "0123456789abcdef", "0123456789abcdef"},
		{"canonical 2-hex passes through", "ab", "ab"},
		{"canonical 64-hex passes through", strings.Repeat("ab", 32), strings.Repeat("ab", 32)},
		{"odd length is hashed", "abc", ""},
		{"single char is hashed", "a", ""},
		{"uppercase is hashed", "ABCDEF12", ""},
		{"non-hex is hashed", "toolchain-v178", ""},
		{"over 64 chars is hashed", strings.Repeat("ab", 33), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := CanonicalKeyNamespace(tc.in)
			want := tc.want
			if want == "" && tc.in != "" {
				sum := sha256.Sum256([]byte(tc.in))
				want = fmt.Sprintf("%x", sum[:8])
			}
			assert.Equal(t, want, got)
			// Every non-empty result must itself be canonical (idempotent).
			if tc.in != "" {
				assert.Equal(t, got, CanonicalKeyNamespace(got), "canonicalization must be idempotent")
				assert.True(t, isCanonicalNamespace(got), "result must be canonical")
			}
		})
	}
}

// TestActionKeyShapes pins the key-shape invariants the namespace design
// relies on: an unnamespaced key is the plain hex action ID (byte-identical
// to historic behavior), a namespaced key is that hex plus the namespace as a
// suffix, and keys from different namespaces (or no namespace) can never be
// equal for fixed-length action IDs.
func TestActionKeyShapes(t *testing.T) {
	raw := bytes.Repeat([]byte{0xab}, 32) // cmd/go action IDs are sha256 digests
	hexKey := fmt.Sprintf("%x", raw)

	plain := &Server{}
	nsA := &Server{}
	nsA.SetKeyNamespace("aaaaaaaaaaaaaaaa")
	nsB := &Server{}
	nsB.SetKeyNamespace("bbbbbbbbbbbbbbbb")

	assert.Equal(t, hexKey, plain.actionKey(raw), "no namespace: key must be byte-identical to the historic hex form")
	assert.Equal(t, hexKey+"aaaaaaaaaaaaaaaa", nsA.actionKey(raw))
	assert.Equal(t, hexKey+"bbbbbbbbbbbbbbbb", nsB.actionKey(raw))
	assert.NotEqual(t, plain.actionKey(raw), nsA.actionKey(raw))
	assert.NotEqual(t, nsA.actionKey(raw), nsB.actionKey(raw))
}

// TestNamespaceSuffixPreservesBuildIDGuard pins why namespacing is a SUFFIX,
// not a hash-combined rewrite: the build-id guard derives its expectation from
// the leading bytes of the hex-decoded key, and a suffix leaves those bytes
// intact, so a compiled package's stamped build id still verifies against the
// real cmd/go action ID. Rewriting the leading bytes would break the guard.
func TestNamespaceSuffixPreservesBuildIDGuard(t *testing.T) {
	raw := bytes.Repeat([]byte{0x5c}, 32)
	hexKey := fmt.Sprintf("%x", raw)

	want := expectedBuildIDAction(hexKey)
	require.NotEmpty(t, want, "a real 32-byte action ID must yield a build-id expectation")

	srv := &Server{}
	srv.SetKeyNamespace("0123456789abcdef")
	assert.Equal(t, want, expectedBuildIDAction(srv.actionKey(raw)),
		"the namespaced key must derive the SAME build-id expectation as the raw key")
}

// TestServerNamespaceIsolation is the cross-toolchain poisoning regression
// test: entries stored under a namespace must be invisible to every other
// namespace (and to unnamespaced clients) on BOTH tiers — the local store and
// the remote backend — in both directions. This is what makes rival
// fork-toolchain builds (whose colliding action IDs caused the summer
// SIGSEGV-APE incident) structurally unable to share cache entries.
func TestServerNamespaceIsolation(t *testing.T) {
	dir := t.TempDir()
	lc, err := NewLocalCache(dir)
	require.NoError(t, err)
	remote := newMemBackend()

	// A single action ID, as computed identically by rival fork toolchain builds (the collision).
	actionID := bytes.Repeat([]byte{0x42}, 32)
	bodyA := "object compiled by toolchain A"
	sumA := sha256.Sum256([]byte(bodyA))

	const nsA = "aaaa111122223333"
	const nsB = "bbbb444455556666"

	// runServer executes a protocol conversation against the SHARED stores
	// under the given namespace ("" = unnamespaced) and returns the responses
	// (handshake leading). Each conversation uses a fresh Server, modeling a
	// fresh build's cacheprog process.
	runServer := func(t *testing.T, namespace, script string) []Response {
		t.Helper()
		srv := NewServer(lc, remote)
		srv.SetKeyNamespace(namespace)
		var out bytes.Buffer
		require.NoError(t, srv.Run(strings.NewReader(script), &out))
		return parseResponses(t, out.Bytes())
	}

	// Toolchain A's build PUTs its object.
	putScript := makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID,
		OutputID: sumA[:], BodySize: int64(len(bodyA)),
	}, bodyA) + makeRequest(Request{ID: 2, Command: CmdClose})
	resps := runServer(t, nsA, putScript)
	require.Len(t, resps, 3) // handshake, put, close
	require.Empty(t, resps[1].Err, "put must succeed")

	// The stored keys, local and remote, must be namespaced, so the entry is unreachable from another namespace.
	keyA := fmt.Sprintf("%x", actionID) + nsA
	_, miss := lc.Peek(keyA)
	require.False(t, miss)

	remote.mu.Lock()
	_, remoteHasA := remote.store[keyA]
	_, remoteHasBare := remote.store[fmt.Sprintf("%x", actionID)]
	remote.mu.Unlock()
	assert.True(t, remoteHasA, "remote tier must hold the entry under the namespaced key")
	assert.False(t, remoteHasBare, "remote tier must NOT hold the entry under the bare (unnamespaced) key")

	getScript := makeRequest(Request{ID: 1, Command: CmdGet, ActionID: actionID}) +
		makeRequest(Request{ID: 2, Command: CmdClose})

	// Toolchain B's build GETs the same action ID: MISS on both tiers.
	resps = runServer(t, nsB, getScript)
	require.Len(t, resps, 3)
	assert.True(t, resps[1].Miss, "namespace-A puts must be invisible to namespace-B gets")

	// An unnamespaced client GETs the same action ID: MISS on both tiers.
	resps = runServer(t, "", getScript)
	require.Len(t, resps, 3)
	assert.True(t, resps[1].Miss, "namespace-A puts must be invisible to unnamespaced gets")

	// A fresh build with toolchain A (same namespace, new process) HITS.
	resps = runServer(t, nsA, getScript)
	require.Len(t, resps, 3)
	require.False(t, resps[1].Miss, "the same namespace must keep hitting its own entries")
	assert.Equal(t, sumA[:], resps[1].OutputID)

	// Reverse direction: an unnamespaced PUT under the same action ID must not touch toolchain A's object.
	bodyU := "object compiled by an unnamespaced foreign build"
	sumU := sha256.Sum256([]byte(bodyU))
	resps = runServer(t, "", makePutRequest(Request{
		ID: 1, Command: CmdPut, ActionID: actionID,
		OutputID: sumU[:], BodySize: int64(len(bodyU)),
	}, bodyU)+makeRequest(Request{ID: 2, Command: CmdClose}))
	require.Empty(t, resps[1].Err)

	resps = runServer(t, nsA, getScript)
	require.False(t, resps[1].Miss)
	assert.Equal(t, sumA[:], resps[1].OutputID,
		"a foreign build's put under the colliding action ID must never displace a namespaced entry")

	// And namespace B still sees nothing of either entry.
	resps = runServer(t, nsB, getScript)
	assert.True(t, resps[1].Miss, "namespace B must see neither A's nor the unnamespaced entry")
}
