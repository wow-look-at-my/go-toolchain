package cache

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

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
// directory read), the safe response to one is to refuse it and let cmd/go
// rebuild it -- never to serve bytes whose provenance under this key cannot be
// established. A false positive (some other payload that happens to start with
// this magic) only costs a recompute, never correctness, so the loose prefix
// match is deliberately conservative.
//
// The refusal covers EVERY tier and both directions. The web paths refuse on
// ingestion (web.go individual GET, batch.go batch GET, the prefetch filter in
// cache.go) and on upload (webput.go). The local tier refuses at the PUT
// (cachehandlers.go), so no index ever enters the store, and again on the serve
// path (verify.go) so residue from an older binary cannot be handed back
// either. Refusing at the serve path ALONE is what must not happen: cmd/go
// re-stores every index it recomputes, so accept-at-Put/refuse-at-Get is a
// permanent miss loop that grows the store on every build (see verify.go).
//
// A refused PUT is not an error. cmd/go treats a failed index store as fatal
// (openIndexModule returns it), and the GOCACHEPROG contract still owes the
// caller a DiskPath naming a file that holds the body until "close" -- so the
// body is written to a scratch sink outside the cache (see Server.sinkIndexBody)
// and the reply names that file. Nothing enters the cache, and the next GET for
// the key misses, which is exactly what makes cmd/go recompute.
func isGoModuleIndex(body []byte) bool {
	return bytes.HasPrefix(body, []byte(goModuleIndexMagic))
}

// sinkIndexBody writes a refused module-index body to this Server's scratch
// sink and returns the file's path.
//
// The GOCACHEPROG "put" reply must name a file holding the body that survives
// until "close" (cmd/go rejects an empty DiskPath outright), so a refusal still
// owes the caller a real file -- it just must not be one the cache can serve
// back. The sink is a private temp directory, removed when the protocol loop
// ends, and nothing ever looks a key up in it. Bodies are content-addressed by
// outputID, so the same index recomputed by several go invocations on one
// connection costs one file.
func (s *Server) sinkIndexBody(outputID string, body []byte) (string, error) {
	s.sinkMu.Lock()
	defer s.sinkMu.Unlock()
	if s.sinkDir == "" {
		dir, err := os.MkdirTemp("", "go-toolchain-modindex-")
		if err != nil {
			return "", err
		}
		s.sinkDir = dir
	}
	f, err := os.CreateTemp(s.sinkDir, ".tmp-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	_, werr := f.Write(body)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp)
		if werr != nil {
			return "", werr
		}
		return "", cerr
	}
	if outputID == "" {
		return tmp, nil
	}
	final := filepath.Join(s.sinkDir, outputID)
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return final, nil
}

// removeIndexSink deletes the scratch sink. Called when the protocol loop ends
// -- after the "close" reply, which is the point the DiskPath contract expires.
func (s *Server) removeIndexSink() {
	s.sinkMu.Lock()
	dir := s.sinkDir
	s.sinkDir = ""
	s.sinkMu.Unlock()
	if dir != "" {
		os.RemoveAll(dir)
	}
}

// refuseIndexPut answers a PUT carrying a module index: the body is sunk
// outside the cache and the reply names it, so cmd/go's contract holds while
// the cache stores nothing (see isGoModuleIndex).
func (s *Server) refuseIndexPut(req Request, actionID, outputID string) Response {
	path, err := s.sinkIndexBody(outputID, req.Body)
	if err != nil {
		// Fail loud: a sink that cannot be written is a broken TMPDIR, and
		// cmd/go must not be told an index was stored when it was not.
		return Response{ID: req.ID, Err: fmt.Sprintf("cacheprog: module-index sink: %v", err)}
	}
	s.IndexPutsRefused.Increment()
	logger.WithSubsystem("cache").Debug("PUT  refuse %s output=%s size=%d (module index: recomputed, never cached)",
		actionID, shortID(outputID), len(req.Body))
	return Response{ID: req.ID, DiskPath: path}
}
