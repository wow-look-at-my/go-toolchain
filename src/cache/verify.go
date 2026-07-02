package cache

import "fmt"

// This file holds the shared serve-path integrity gates used by the local
// cache tiers. The pack store (pack.go) runs its gates over pack records; the
// loose-file tier (local.go) runs verifyBodyForServe over each entry before
// serving it. Both enforce the same invariant the web ingestion path already
// does: the cache never hands the compiler a body it cannot tie to the
// requested action key.

// verifyBodyForServe runs the local serve-path integrity gates over an
// in-memory body about to be served for (actionID, outputID):
//
//   - content address: the body must hash (SHA-256) to outputID — the
//     GOCACHEPROG invariant (outputID == sha256(body), see outputIDMatches).
//     Catches truncation, rot, and mis-mapped bodies, including the empty
//     bodies the old oversized-PUT bug committed under real IDs.
//   - build-id action: a compiled package's stamped build-id action must
//     belong to actionID (see buildIDMatchesAction). Catches a self-consistent
//     object filed under the wrong action key ("runtime imported as
//     reflectlite").
//   - module index: a Go module index is never served from cache (see
//     isGoModuleIndex) — it is unverifiable under any key and catastrophic if
//     mis-keyed; cmd/go recomputes it locally for free.
//
// On failure it returns ok == false and a human-readable reason for the
// eviction log line.
func verifyBodyForServe(actionID, outputID string, body []byte) (reason string, ok bool) {
	if got, ok := outputIDMatches(outputID, body); !ok {
		return fmt.Sprintf("body checksum mismatch (want outputid=%s, got sha256=%s, len=%d)",
			shortID(outputID), shortID(got), len(body)), false
	}
	if act, ok := buildIDMatchesAction(actionID, body); !ok {
		return fmt.Sprintf("build-id action mismatch (want action=%s, got action=%s, len=%d)",
			expectedBuildIDAction(actionID), act, len(body)), false
	}
	if isGoModuleIndex(body) {
		return fmt.Sprintf("module-index blob (unverifiable under this key, len=%d)", len(body)), false
	}
	return "", true
}

// looseVerified records a loose-tier entry that passed verifyBodyForServe
// this process, so repeat Gets (notably the PUT dedup check on warm rebuilds)
// skip the body re-read + re-hash. An entry only short-circuits verification
// while the on-disk sidecar still advertises the same outputID and the data
// file still has the verified size; any replacement re-verifies.
type looseVerified struct {
	outputID string
	size     int64
}
