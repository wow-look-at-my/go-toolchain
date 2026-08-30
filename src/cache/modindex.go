package cache

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// sinkIndexBody writes a refused module-index body to this Server's scratch
// sink and returns the file's path.
//
// The GOCACHEPROG "put" reply must name a file holding the body that survives
// until "close" (cmd/go rejects an empty DiskPath outright), so a refusal still
// owes the caller a real file -- it just must not be a file the cache can serve
// back. The sink is a private temp directory, removed when the protocol loop
// ends, and nothing ever looks a key up in it. Bodies are content-addressed by
// outputID, so the same index recomputed by several go invocations on a
// connection costs a single file.
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

// removeIndexSink deletes the scratch sink after the protocol loop's close reply, when DiskPath's contract expires.
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
		// Fail loud: a broken TMPDIR must not be reported to cmd/go as a stored index.
		return Response{ID: req.ID, Err: fmt.Sprintf("cacheprog: module-index sink: %v", err)}
	}
	s.IndexPutsRefused.Increment()
	logger.WithSubsystem("cache").Debug("PUT  refuse %s output=%s size=%d (module index: recomputed, never cached)",
		actionID, shortID(outputID), len(req.Body))
	return Response{ID: req.ID, DiskPath: path}
}
