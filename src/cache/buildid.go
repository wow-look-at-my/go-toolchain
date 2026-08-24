package cache

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
)

// buildIDHashSize is the leading action-hash bytes cmd/go stamps into a build id's ACTION field: base64.RawURLEncoding(hash[:15]).
const buildIDHashSize = 15

// expectedBuildIDAction derives the build-id ACTION field cmd/go would stamp
// for cache action actionIDHex (base64.RawURLEncoding(actionID[:15])).
// Returns "" when actionIDHex is too short to derive one -- not a mismatch.
func expectedBuildIDAction(actionIDHex string) string {
	raw, err := hex.DecodeString(actionIDHex)
	if err != nil || len(raw) < buildIDHashSize {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw[:buildIDHashSize])
}

// archiveExportInfo inspects data as a Go compiled-package object and reports:
//
//   - isPkgArchive: data is an ar archive with a __.PKGDEF export-data member --
//     i.e. it presents as a loadable Go package, the shape this guard exists to
//     check.
//   - action: the action field of its build id (before '/' in
//     `build id "ACTION/CONTENT"`), or "" if no build id line.
//
// A non-archive (vet facts, stdout, source lists, empty output) yields
// (false, ""). Only the text header before the "$$" export-data marker is
// scanned, so binary export bytes can't be mistaken for a build id line.
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

// archiveBuildIDAction returns the ACTION field of data's build id (see
// archiveExportInfo), or "" if data has no build id line.
func archiveBuildIDAction(data []byte) string {
	_, action := archiveExportInfo(data)
	return action
}

// buildIDMatchesAction reports whether body is consistent with cache action
// actionIDHex. This is a check outputIDMatches cannot make: a body can be
// self-consistent yet stored under the wrong action key (e.g. reflectlite
// export data served for the runtime action), poisoning the build with
// "imported as reflectlite". A compiled package's build id carries its own
// action key (base64.RawURLEncoding(actionID[:15])), so it can be verified.
//
// got is the archive's stamped action ("" if none). ok is false when the
// build id proves a DIFFERENT action than requested, or the object is a
// package archive with NO build id at all -- cmd/go always stamps one, so a
// missing build id means corruption or a stripped mismatch. ok is true when
// the object is not a package archive, or no expectation can be derived from
// actionIDHex.
//
// This is best-effort integrity, not an authorization boundary: a writer that
// forges a matching build id still passes. See docs/CACHE.md.
func buildIDMatchesAction(actionIDHex string, body []byte) (got string, ok bool) {
	isPkg, action := archiveExportInfo(body)
	want := expectedBuildIDAction(actionIDHex)

	if action != "" {
		if want == "" {
			return action, true // no derivable expectation; don't false-positive
		}
		return action, action == want
	}

	// A package archive with no build id, checked against a real key, is refused.
	// A non-archive entry falls through, guarded only by the hash.
	if isPkg && want != "" {
		return "", false
	}
	return "", true
}
