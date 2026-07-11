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

// archiveExportInfo inspects data as a Go compiled-package object and reports:
//
//   - isPkgArchive: data is an ar archive carrying a __.PKGDEF export-data
//     member -- i.e. it presents as a loadable Go package, the only shape an
//     object can have if a build is going to consume it as a package's export
//     data (the failure mode this whole guard exists to stop).
//   - action: the action field of its build id (the part before '/' in the
//     `build id "ACTION/CONTENT"` header line), or "" if the archive carries no
//     build id line.
//
// A non-archive (vet facts, command stdout, source-file lists, empty output)
// yields (false, ""). Only the text header preceding the "$$" export-data
// marker is scanned for the build id, so binary export bytes can never be
// mistaken for a build id line.
func archiveExportInfo(data []byte) (isPkgArchive bool, action string) {
	pkgdef := arMember(data, "__.PKGDEF")
	if pkgdef == nil {
		return false, ""
	}
	header := pkgdef
	if i := bytes.Index(header, []byte("$$")); i >= 0 {
		header = header[:i]
	}
	const marker = `build id "`
	idx := bytes.Index(header, []byte(marker))
	if idx < 0 {
		return true, ""
	}
	rest := header[idx+len(marker):]
	end := bytes.IndexByte(rest, '"')
	if end < 0 {
		return true, ""
	}
	id := rest[:end]
	if slash := bytes.IndexByte(id, '/'); slash >= 0 {
		id = id[:slash]
	}
	return true, string(id)
}

// archiveBuildIDAction returns the action field of the build id the Go toolchain
// stamps into a compiled package's __.PKGDEF header, e.g.
// `build id "ACTION/CONTENT"` -> "ACTION". It returns "" when data is not a Go
// ar archive, or carries no build id line -- the case for every cache entry
// that is not a build-id-stamped compiled package (vet facts, command stdout,
// source-file lists, or an archive produced by `go tool compile` without
// stamping). A thin accessor over archiveExportInfo.
func archiveBuildIDAction(data []byte) string {
	_, action := archiveExportInfo(data)
	return action
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
//   - ok == false when the object carries a build id that proves it belongs to a
//     DIFFERENT action than requested (proven cross-contamination), OR when it
//     presents as a Go package archive yet carries NO build id at all. The Go
//     toolchain always stamps a build id into a compiled package, so a package
//     archive without one is corrupt or has been deliberately stripped to slip a
//     different package's export data under this key -- exactly the evasion a
//     bare archiveBuildIDAction=="" pass-through would allow. Refusing it closes
//     that gap. (This is best-effort integrity, not an authorization boundary: a
//     writer who forges a build id matching the target action still passes, so a
//     shared cache must also control who may write -- see docs/CACHE.md.)
//   - ok == true when the object is not a package archive at all (nothing to
//     verify; the body<->outputID hash remains the gate), or when no expectation
//     can be derived from actionIDHex.
func buildIDMatchesAction(actionIDHex string, body []byte) (got string, ok bool) {
	isPkg, action := archiveExportInfo(body)
	want := expectedBuildIDAction(actionIDHex)

	if action != "" {
		if want == "" {
			return action, true // no derivable expectation; don't false-positive
		}
		return action, action == want
	}

	// No build id parsed. A package archive must be build-id-stamped; one that
	// is not -- when we have a real key to check it against -- is refused. Plain
	// non-archive entries fall through and remain guarded only by the hash.
	if isPkg && want != "" {
		return "", false
	}
	return "", true
}
