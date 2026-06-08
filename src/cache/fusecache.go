//go:build linux || darwin

package cache

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// FuseCache is a LocalStore that keeps object bodies in append-only pack files
// (a PackStore) and exposes them through a read-only FUSE mount.
//
// This is the "virtual filesystem over the JSON protocol." The GOCACHEPROG GET
// response hands the Go toolchain a DiskPath, which the compiler opens itself —
// but nothing requires that path to be a real file. FuseCache returns
// DiskPath = <mnt>/<outputID>; when the compiler opens it, the kernel routes
// the read to this process, which serves the bytes straight out of a pack file
// via ReadAt. No loose file and no metadata sidecar is ever written per entry,
// so the tiny-file explosion that dominates a build cache disappears.
type FuseCache struct {
	store    *PackStore
	mnt      string
	server   *fuse.Server
	lockFile *os.File // held for the cache's lifetime; flock makes us the sole owner
}

// newFuseCache opens the pack store under cacheDir/packs and mounts a read-only
// FUSE filesystem at cacheDir/mnt.
//
// It first takes an exclusive, non-blocking flock on cacheDir/.fuse.lock. Only
// the lock holder owns the mount and pack store; a second caller on the same
// cacheDir gets errFuseBusy and falls back to the loose cache. This is the
// safety interlock that prevents a nested go-toolchain run (e.g. a test that
// calls enableCacheProg, inheriting the same XDG_CACHE_HOME) from unmounting
// the live daemon's mount via the stale-cleanup unmount below.
func newFuseCache(cacheDir string) (fuseStore, error) {
	mnt := filepath.Join(cacheDir, "mnt")
	packsDir := filepath.Join(cacheDir, "packs")

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, err
	}
	lockFile, err := os.OpenFile(filepath.Join(cacheDir, ".fuse.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockFile.Close()
		// Only a would-block means another live owner holds the lock (-> fall
		// back to loose quietly). Anything else (EBADF, permissions, fs issues)
		// is a real error worth surfacing rather than masking as "busy".
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errFuseBusy
		}
		return nil, fmt.Errorf("fuse cache lock %s: %w", filepath.Join(cacheDir, ".fuse.lock"), err)
	}
	// We hold the lock, so any mount still on mnt is stale (left by a crashed
	// owner whose flock the kernel already released). Clear it before mounting.
	// This unmount is safe precisely because we own the lock — no live owner.
	_ = syscall.Unmount(mnt, unmountDetach)

	if err := os.MkdirAll(mnt, 0o755); err != nil {
		releaseLock(lockFile)
		return nil, err
	}
	store, err := OpenPackStore(packsDir)
	if err != nil {
		releaseLock(lockFile)
		return nil, err
	}

	// Cache timeouts are tuned for a content-addressed store that only ever
	// *grows* during a build:
	//   - Positive entries are immutable, so cache them aggressively.
	//   - Negative lookups must NOT be cached: a name absent now (a body not yet
	//     produced) may exist moments later, and the compiler will re-open it.
	//     Caching "not found" would make a freshly-Put body invisible for the
	//     timeout window — exactly the ENOENT-on-compile bug a parallel build
	//     triggers.
	entryTimeout := time.Hour
	negativeTimeout := time.Duration(0)
	root := &fuseRoot{store: store}
	server, err := fs.Mount(mnt, root, &fs.Options{
		EntryTimeout:    &entryTimeout,
		AttrTimeout:     &entryTimeout,
		NegativeTimeout: &negativeTimeout,
		MountOptions: fuse.MountOptions{
			// DirectMount uses the mount(2) syscall (works as root, e.g. in
			// containers); go-fuse falls back to the fusermount helper when
			// that fails (works for non-root CI runners). Not Strict, so the
			// fallback is allowed.
			DirectMount: true,
			FsName:      "go-toolchain-cache",
			Name:        "gtcache",
			// Big reads + readahead: the compiler mmaps cached archives, so each
			// page fault becomes a read here. Larger max_read and readahead let
			// the kernel pull big chunks per round-trip, which is what keeps a
			// warm (all-hits) build fast despite the FUSE indirection.
			MaxWrite:      1 << 20,
			MaxReadAhead:  1 << 20,
			DisableXAttrs: true,
			Debug:         os.Getenv("GOCACHE_FUSE_GODEBUG") == "1",
		},
	})
	if err != nil {
		store.Close()
		releaseLock(lockFile)
		return nil, fmt.Errorf("fuse mount %s: %w", mnt, err)
	}
	return &FuseCache{store: store, mnt: mnt, server: server, lockFile: lockFile}, nil
}

// releaseLock drops the flock and closes the lock file.
func releaseLock(f *os.File) {
	if f == nil {
		return
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	_ = f.Close()
}

func (c *FuseCache) mountInfo() string {
	return fmt.Sprintf("mount=%s packs=%d", c.mnt, c.store.Len())
}

// Get resolves actionID to a DiskPath inside the FUSE mount. The body is
// integrity-checked first (see PackStore.GetVerified): a corrupt body is evicted
// and reported as a miss rather than handed to the toolchain.
func (c *FuseCache) Get(actionID string) (CacheMeta, bool) {
	loc, ok := c.store.GetVerified(actionID)
	if !ok {
		return CacheMeta{}, true
	}
	return CacheMeta{
		OutputID: loc.outputID,
		Size:     loc.dataLen,
		Time:     time.Unix(loc.created, 0),
		DiskPath: filepath.Join(c.mnt, loc.outputID),
	}, false
}

// Put stores body and returns a DiskPath inside the FUSE mount that the Go
// toolchain can open.
func (c *FuseCache) Put(actionID, outputID string, body io.Reader) (string, error) {
	loc, err := c.store.Put(actionID, outputID, body)
	if err != nil {
		return "", err
	}
	return filepath.Join(c.mnt, loc.outputID), nil
}

// StatsPtr returns the underlying pack store's hit/put counters.
func (c *FuseCache) StatsPtr() *CacheStats { return &c.store.Stats }

// Close unmounts the filesystem and closes the pack store. Unmount must precede
// the pack-store close so in-flight kernel reads can't hit closed handles.
//
// go-fuse's Unmount shells out to the fusermount helper, which is absent in
// root/direct-mount environments (containers, this CI). When it fails we unmount
// with the unmount(2) syscall directly (we're root if there's no fusermount),
// then wait for the serve loop to drain once /dev/fuse closes.
func (c *FuseCache) Close() error {
	var firstErr error
	if c.server != nil {
		// Try a clean unmount (fusermount helper), then the unmount(2) syscall
		// (we're root where there's no helper), then a lazy detach. Wait() for
		// the serve loop only if the mount actually came down — once /dev/fuse
		// closes the loop returns; if every attempt failed, Wait() would block
		// forever, so we skip it.
		unmounted := false
		if err := c.server.Unmount(); err == nil {
			unmounted = true
		} else if err := syscall.Unmount(c.mnt, 0); err == nil {
			unmounted = true
		} else if err := syscall.Unmount(c.mnt, unmountDetach); err == nil {
			unmounted = true
		} else {
			firstErr = fmt.Errorf("unmount %s failed: %w", c.mnt, err)
		}
		if unmounted {
			c.server.Wait() // drain the serve loop before closing pack handles
		}
	}
	if err := c.store.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	releaseLock(c.lockFile) // release ownership so the next daemon can take over
	return firstErr
}

// fuseRoot is the mount's root directory. It resolves a child name (an
// outputID hex string) to a file node backed by the pack store.
type fuseRoot struct {
	fs.Inode
	store *PackStore
}

var _ = (fs.NodeLookuper)((*fuseRoot)(nil))

func (r *fuseRoot) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// GetByOutputVerified, not GetByOutput: this is the path the compiler reads
	// through, so the body's CRC is checked here before it is exposed. A corrupt
	// body is evicted and reported as ENOENT (a miss) rather than handed to the
	// toolchain — the serve-path counterpart to GetVerified on the GET RPC.
	loc, ok := r.store.GetByOutputVerified(name)
	if !ok {
		return nil, syscall.ENOENT
	}
	out.Attr.Mode = 0o444
	out.Attr.Size = uint64(loc.dataLen)
	// A stable inode per outputID (content hash) makes the kernel's dentry/page
	// caching coherent: the same body always maps to the same inode, so the
	// FOPEN_KEEP_CACHE pages are never reused across distinct contents.
	stable := fs.StableAttr{Mode: fuse.S_IFREG, Ino: inoFor(name)}
	child := r.NewInode(ctx, &fuseFile{store: r.store, loc: loc}, stable)
	return child, 0
}

// inoFor derives a stable, non-zero inode number from an outputID.
func inoFor(name string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(name))
	if v := h.Sum64(); v != 0 {
		return v
	}
	return 1
}

// fuseFile serves one cached body, reading on demand from the pack store.
type fuseFile struct {
	fs.Inode
	store *PackStore
	loc   packLoc
}

var (
	_ = (fs.NodeGetattrer)((*fuseFile)(nil))
	_ = (fs.NodeOpener)((*fuseFile)(nil))
	_ = (fs.NodeReader)((*fuseFile)(nil))
)

func (f *fuseFile) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0o444
	out.Size = uint64(f.loc.dataLen)
	return 0
}

func (f *fuseFile) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// Reject writes: this is a read-only cache view.
	if int(flags)&(os.O_WRONLY|os.O_RDWR) != 0 {
		return nil, 0, syscall.EROFS
	}
	// Bodies are immutable, so the kernel may cache pages across opens.
	return nil, fuse.FOPEN_KEEP_CACHE, 0
}

func (f *fuseFile) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	// Zero-copy: hand the kernel the pack fd + offset so it reads/splices the
	// bytes directly, instead of copying them through the daemon. This is what
	// keeps a warm, all-hits build fast despite the FUSE indirection — the
	// compiler mmaps these archives, so every page fault is a read here.
	fd, absOff, avail := f.store.fdForRead(f.loc, off)
	if avail <= 0 {
		return fuse.ReadResultData(nil), 0
	}
	sz := int64(len(dest))
	if sz > avail {
		sz = avail
	}
	return fuse.ReadResultFd(fd, absOff, int(sz)), 0
}
