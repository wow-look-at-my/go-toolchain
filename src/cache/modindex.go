package cache

import "bytes"

// goModuleIndexMagic is the leading bytes of a Go module index blob. cmd/go's
// modindex writer stamps a version line "go index vN\n" at the head of every
// index it stores (see cmd/go/internal/modindex: indexVersion is "go index v2",
// written verbatim followed by '\n'). Matching the version-less prefix keeps the
// check correct across index format bumps (v1, v2, future vN).
const goModuleIndexMagic = "go index v"

// isGoModuleIndex reports whether body is a Go module index blob -- the on-disk
// package/directory index that cmd/go stores and retrieves THROUGH the build
// cache (cache.Default() is the GOCACHEPROG when one is set, so PutBytes/GetMmap
// for the index flow over this protocol just like compiled objects do).
//
// The module index is the one cached payload that is both (a) unverifiable
// against its action key and (b) catastrophic if mis-keyed:
//
//   - Unverifiable: unlike a compiled package archive, an index blob carries no
//     build id, and it does not embed the directory it indexes in any form that
//     can be checked against the requested action key (a dirHash over the Go
//     version + path + file mtimes/sizes). outputIDMatches proves only that the
//     body hashes to its advertised outputID -- internally self-consistent even
//     for a blob filed under the wrong key -- and buildIDMatchesAction is a
//     no-op on a non-archive. So a wrong-but-well-formed index served under a
//     key cannot be detected, only refused.
//
//   - Catastrophic: the go command consults the index at package-load time via
//     IsGoDir(). An index for a directory with no Go files served for, say,
//     $GOROOT/src/runtime makes the loader report "package runtime is not in
//     std" and fail the build before compilation even starts; a truncated or
//     cross-version one yields "corrupt index". Neither is recoverable in-process.
//
// Because an index is cheap for the go command to recompute locally (a single
// directory read), the safe response to one arriving from the shared remote
// cache is to refuse it and let cmd/go rebuild it -- never to serve bytes whose
// provenance under this key cannot be established. A false positive (some other
// payload that happens to start with this magic) only costs a recompute, never
// correctness, so the loose prefix match is deliberately conservative.
//
// The refusal is scoped to the SHARED tier: every web->local ingestion path
// (web.go individual GET, batch.go batch GET, the prefetch filter in cache.go)
// and the upload path (webput.go) consult this predicate. The LOCAL tiers do
// NOT -- an index in the local store was, post the one-time version purge
// (cacheversion.go), stored there by the local cmd/go under its own action key,
// and is served back exactly like upstream GOCACHE serves its own directory
// (see verify.go's file-top comment for the store/refuse loop that scoping
// this wrongly caused).
func isGoModuleIndex(body []byte) bool {
	return bytes.HasPrefix(body, []byte(goModuleIndexMagic))
}
