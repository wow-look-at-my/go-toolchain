package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// outputIDMatches reports whether body's content matches the expected outputID,
// also returning the body's actual hex SHA-256 (got) for diagnostics either way.
//
// In the GOCACHEPROG contract a cache entry's outputID IS the SHA-256 of its
// body: the go command computes it as sha256(output), and the server derives the
// stored id as fmt.Sprintf("%x", req.OutputID) (see Server.handlePut). The pack
// store's content-addressed dedup (PackStore.byOutput) already relies on this
// invariant. So a body whose SHA-256 does not equal its advertised outputID is
// corrupt, and must never be materialized into the local pack or served.
//
// This is the integrity gate the local pack CRC cannot provide. That CRC
// (PackStore.GetVerified) is computed in appendRecordLoc from whatever bytes are
// handed to Put, so it only catches corruption that happens AFTER a good body is
// stored (disk/overlay bit-rot). A body that is already corrupt when it arrives
// from the remote tier — a truncated download, a bad LZ4 decode, or a
// poisoned/rotted remote object — would be stored with a self-consistent CRC and
// then served as "valid" on every future hit, surfacing in the go command as an
// unrecoverable "corrupt index" build failure that persists across runs and
// machines (one bad remote object poisons every consumer). Verifying end-to-end
// here, at the network boundary, against the content hash go itself computed is
// what stops a corrupt remote object from ever being trusted.
//
// The comparison is case-insensitive purely as defense against an intermediary
// upper-casing the hex during the outputid metadata-header round-trip; both
// sides are normally lowercase.
func outputIDMatches(outputID string, body []byte) (got string, ok bool) {
	sum := sha256.Sum256(body)
	got = hex.EncodeToString(sum[:])
	return got, strings.EqualFold(got, outputID)
}
