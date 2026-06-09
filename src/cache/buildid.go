package cache

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
)

// buildIDHashSize is the number of leading action-hash bytes the Go toolchain
// encodes into a build id's action field. cmd/internal/buildid.HashToString
// renders a cache hash as base64.RawURLEncoding(hash[:15]); the first
// slash-separated field of a compiled object's `build id "ACTION/CONTENT"` is
// exactly that rendering of the action's cache key. 15 bytes -> 20 base64 chars.
const buildIDHashSize = 15

// expectedBuildIDAction returns the build-id action field the Go toolchain
// stamps into a package compiled under the cache action whose key is
// actionIDHex (the 64-hex SHA-256 the cacheprog is asked for). Go derives it as
// base64.RawURLEncoding(actionID[:15]) -- see buildIDHashSize. Returns "" when
// actionIDHex is not at least buildIDHashSize bytes of valid hex, in which case
// no expectation can be derived and the caller must not treat anything as a
// mismatch.
func expectedBuildIDAction(actionIDHex string) string {
	raw, err := hex.DecodeString(actionIDHex)
	if err != nil || len(raw) < buildIDHashSize {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw[:buildIDHashSize])
}

// archiveBuildIDAction extracts the action field (the part before the first
// '/') of the build id the Go toolchain stamps into a compiled package's
// __.PKGDEF header, e.g. `build id "ACTION/CONTENT"` -> "ACTION". It returns ""
// when data is not a Go ar archive, or carries no build id line -- which is the
// case for every cache entry that is not a build-id-stamped compiled package
// (vet facts, command stdout, source-file lists, or an archive produced by
// `go tool compile` without stamping). Only the text header preceding the "$$"
// export-data marker is scanned, so binary export bytes can never be mistaken
// for a build id line.
func archiveBuildIDAction(data []byte) string {
	pkgdef := arMember(data, "__.PKGDEF")
	if pkgdef == nil {
		return ""
	}
	// The build id lives in the leading text header; the binary export data
	// follows the "$$" marker. Bounding the search there keeps stray export
	// bytes from ever matching the marker below.
	if i := bytes.Index(pkgdef, []byte("$$")); i >= 0 {
		pkgdef = pkgdef[:i]
	}
	const marker = `build id "`
	idx := bytes.Index(pkgdef, []byte(marker))
	if idx < 0 {
		return ""
	}
	rest := pkgdef[idx+len(marker):]
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	id := rest[:end]
	if slash := bytes.IndexByte(id, '/'); slash >= 0 {
		id = id[:slash]
	}
	return string(id)
}

// buildIDMatchesAction reports whether body is consistent with being the output
// of the cache action identified by actionIDHex. This is the cross-check that
// the body<->outputID hash (outputIDMatches) cannot make: that hash proves a
// body is internally consistent with its advertised content id, but NOT that
// the content belongs under the requested action key. A self-consistent object
// stored (or mapped) under the wrong action key -- e.g. the internal/reflectlite
// export data served for the `runtime` action -- passes the hash check yet
// poisons the build with "imported as reflectlite". A compiled package carries
// its own action key in its build id (the field before '/' is
// base64.RawURLEncoding(actionID[:15])), so it can be matched against the key
// the cacheprog was actually asked for.
//
// It returns the archive's stamped action field (got; "" if none) and ok:
//   - ok == false ONLY when the object carries a build id proving it belongs to
//     a DIFFERENT action than requested -- proven cross-contamination.
//   - ok == true when the object is not a build-id-stamped archive, or when no
//     expectation can be derived from actionIDHex: there is nothing to verify
//     here and the body<->outputID hash remains the integrity gate.
func buildIDMatchesAction(actionIDHex string, body []byte) (got string, ok bool) {
	got = archiveBuildIDAction(body)
	if got == "" {
		return "", true
	}
	want := expectedBuildIDAction(actionIDHex)
	if want == "" {
		return got, true
	}
	return got, got == want
}
