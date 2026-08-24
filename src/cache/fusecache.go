//go:build (linux && !cosmo) || darwin

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

// FuseCache is a LocalStore backed by a PackStore, served through a read-only
// FUSE mount: DiskPath is a real path, with no loose file per cache entry.
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
	lockFile, err := os.OpenFile(filepath.Join(cacheDir, fuseLockName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lockFile.Close()
		// EWOULDBLOCK means another live owner holds the lock: fall back to
		// loose. Any other error is real and must surface, not read as "busy".
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errFuseBusy
		}
		return nil, fmt.Errorf("fuse cache lock %s: %w", filepath.Join(cacheDir, fuseLockName), err)
	}
	// Holding the lock means any mount left on mnt is stale; clear it first.
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

	// Positive entries cache long (immutable); a miss must never cache.
	entryTimeout := time.Hour
	negativeTimeout := time.Duration(0)
	root := &fuseRoot{store: store}
	server, err := fs.Mount(mnt, root, &fs.Options{
		EntryTimeout:    &entryTimeout,
		AttrTimeout:     &entryTimeout,
		NegativeTimeout: &negativeTimeout,
		MountOptions: fuse.MountOptions{
			// mount(2) direct as root; falls back to fusermount for non-root runners.
			DirectMount: true,
			FsName:      "go-toolchain-cache",
			Name:        "gtcache",
			// Big max_read/readahead: the compiler mmaps archives, so each page fault reads here.
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

// Get resolves actionID to a DiskPath inside the FUSE mount. A corrupt body
// (see PackStore.GetVerified) is evicted and reported as a miss.
func (c *FuseCache) Get(actionID string) (CacheMeta, bool) {
	loc, ok := c.store.GetVerified(actionID)
	if !ok {
		return CacheMeta{}, true
	}
	return c.metaFor(loc), false
}

// Peek is Get without counting a hit — see LocalStore.Peek.
func (c *FuseCache) Peek(actionID string) (CacheMeta, bool) {
	loc, ok := c.store.PeekVerified(actionID)
	if !ok {
		return CacheMeta{}, true
	}
	return c.metaFor(loc), false
}

func (c *FuseCache) metaFor(loc packLoc) CacheMeta {
	return CacheMeta{
		OutputID: loc.outputID,
		Size:     loc.dataLen,
		Time:     time.Unix(loc.created, 0),
		DiskPath: filepath.Join(c.mnt, loc.outputID),
	}
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

// PutIfAbsent stores body only if actionID is uncached — see LocalStore.PutIfAbsent.
// The check and store are atomic: a prefetch can't displace a concurrent PUT.
func (c *FuseCache) PutIfAbsent(actionID, outputID string, body io.Reader) (bool, error) {
	_, stored, err := c.store.PutIfAbsent(actionID, outputID, body)
	return stored, err
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
		// Try a clean unmount, then unmount(2), then a lazy detach.
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
	// Verified: the compiler reads through here, so a corrupt body must evict as ENOENT, not be served.
	loc, ok := r.store.GetByOutputVerified(name)
	if !ok {
		return nil, syscall.ENOENT
	}
	out.Attr.Mode = 0o444
	out.Attr.Size = uint64(loc.dataLen)
	// A stable inode per outputID keeps kernel page caching coherent across opens.
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
	// Zero-copy: hand the kernel the pack fd + offset instead of copying through the daemon.
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
