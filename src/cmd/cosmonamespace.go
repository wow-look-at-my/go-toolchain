package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// forkToolchainCacheNamespace derives the cache key namespace for builds
// done with the gosmopolitan fork toolchain at goroot: 16 hex chars of a
// SHA-256 over the toolchain's tool binaries. Every fork-toolchain matrix
// job exports it as GO_TOOLCHAIN_CACHE_NAMESPACE so its cacheprog scopes
// cache keys to THIS toolchain build.
//
// Why: the fork stamps a constant release version into every build, so
// cmd/go derives identical tool build IDs across different fork builds,
// letting a shared cache mix objects across toolchains. Hashing the tool
// binaries catches what those IDs miss: any byte difference changes the
// namespace. Source-tree differences need no hashing here.
//
// The hash covers VERSION plus every file under bin/ and pkg/tool/, framed
// with its slash-relative path and size, in WalkDir's lexical order.
// Failure is an error, never silent: an un-namespaced build would reopen
// cross-toolchain cache poisoning.
func forkToolchainCacheNamespace(goroot string) (string, error) {
	start := time.Now()
	h := sha256.New()

	// VERSION may be absent; frame that explicitly rather than failing, since the tool binaries below are load-bearing.
	version, err := os.ReadFile(filepath.Join(goroot, "VERSION"))
	switch {
	case err == nil:
		hashFrame(h, "VERSION", version)
	case os.IsNotExist(err):
		io.WriteString(h, "VERSION\x00absent\x00")
	default:
		return "", fmt.Errorf("reading %s: %w", filepath.Join(goroot, "VERSION"), err)
	}

	var files int
	for _, dir := range []string{"bin", filepath.Join("pkg", "tool")} {
		root := filepath.Join(goroot, dir)
		n, err := hashTreeInto(h, root, dir)
		if err != nil {
			return "", fmt.Errorf("hashing toolchain %s: %w", dir, err)
		}
		if n == 0 {
			return "", fmt.Errorf("no tool binaries found under %s — not a usable toolchain GOROOT", root)
		}
		files += n
	}

	ns := hex.EncodeToString(h.Sum(nil)[:8])
	logger.Debug("cosmo-bootstrap: cache namespace %s (%d tool files hashed in %s)", ns, files, fmtDuration(time.Since(start)))
	return ns, nil
}

// hashTreeInto hashes every regular file under root into h, framed with its
// slash-relative path (prefixed by rel, the GOROOT-relative name of root) and
// size. Returns the number of files hashed. WalkDir visits entries in lexical
// order, so the digest is deterministic for a given tree.
func hashTreeInto(h io.Writer, root, rel string) (int, error) {
	files := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		sub, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hashFrame(h, filepath.ToSlash(filepath.Join(rel, sub)), data)
		files++
		return nil
	})
	return files, err
}

// hashFrame writes one length-framed file record: path, NUL, decimal size,
// NUL, content. The framing makes file boundaries unambiguous, so content
// cannot alias across concatenated files or paths.
func hashFrame(h io.Writer, name string, data []byte) {
	io.WriteString(h, name)
	io.WriteString(h, "\x00")
	io.WriteString(h, fmt.Sprintf("%d", len(data)))
	io.WriteString(h, "\x00")
	h.Write(data)
}

// daemonSockUnlessNamespaced returns "" when namespaced, since the shared
// daemon's byte-pipe proxy cannot carry a namespace.
func daemonSockUnlessNamespaced(namespace string) string {
	if namespace != "" {
		return ""
	}
	return os.Getenv("GOCACHE_DAEMON_SOCK")
}
