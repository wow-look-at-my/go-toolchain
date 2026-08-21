package cache

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// packNow returns the current unix time. It is a var so tests can pin it.
var packNow = func() int64 { return time.Now().Unix() }

// PackStore is a content-addressed object store that keeps every body in a
// small number of append-only "pack" files instead of one file per entry. It
// is the storage engine behind FuseCache, but it is deliberately FUSE-free and
// portable so it can be unit-tested on any platform.
//
// Why pack at all? A build cache is dominated by tiny entries — source-file
// lists, vet facts, export-data stubs — each far smaller than a filesystem
// block. Stored one-file-per-entry they waste an inode, a directory entry, and
// (rounding up to a block) most of a 4 KiB block apiece. Concatenating them
// into pack files collapses thousands of tiny files into a handful of large
// ones.
//
// On-disk record layout (all integers little-endian):
//
//	magic     uint32  = packRecordMagic ("GTPR")
//	actionID  [32]byte
//	outputID  [32]byte
//	created   int64   (unix seconds)
//	dataLen   uint64
//	crc32     uint32  (IEEE crc of the body, verified before the body is served)
//	data      [dataLen]byte
//
// The format is self-describing: the in-memory index is rebuilt purely by
// scanning the packs at startup, so there is no separate index file to keep in
// sync. A torn final record (from a crash mid-append) is detected — its
// declared length runs past end-of-file — and ignored, so the store is
// crash-safe by construction.
type PackStore struct {
	dir   string
	Stats CacheStats

	mu       sync.RWMutex
	byAction map[string]packLoc // actionID hex -> location
	byOutput map[string]packLoc // outputID hex -> location (content dedup)

	wmu        sync.Mutex // serializes appends to the active pack
	activeID   int
	activeSize int64

	pmu   sync.RWMutex
	packs map[int]*os.File // packID -> open RW handle (used for ReadAt and append)

	// verified memoizes per-record serve-gate facts so warm GETs and FUSE
	// lookups don't re-read + re-hash bodies on every access (see verify.go).
	verified verifiedSet
}

// packLoc records where a body lives: which pack file, the byte offset of the
// body (past the record header), and its length. created/outputID travel with
// it so a Get can answer without touching disk.
type packLoc struct {
	packID   int
	dataOff  int64
	dataLen  int64
	created  int64
	outputID string
	crc      uint32 // IEEE crc32 of the body, from the record header; verified on read
}

const (
	// packRecordMagic ("GTPR") marks the start of a full record (header + body).
	// A scan that reads neither this nor packAliasMagic has hit garbage or a
	// torn write and stops.
	packRecordMagic = 0x47545052
	// packAliasMagic ("GTAL") marks an alias record: a header with no body that
	// maps an actionID onto an outputID whose body is already stored. This is how
	// content dedup is persisted — without it, a deduped action would live only
	// in memory and vanish on restart, missing on the next build.
	packAliasMagic = 0x4754414c
	// packHeaderLen is the fixed header size preceding each body:
	// magic(4) + actionID(32) + outputID(32) + created(8) + dataLen(8) + crc(4).
	packHeaderLen = 4 + 32 + 32 + 8 + 8 + 4
	// hashLen is the byte length of an action/output ID (SHA-256).
	hashLen = 32
	// packFilePrefix/Suffix name the pack files: pack-000001.data, ...
	packFilePrefix = "pack-"
	packFileSuffix = ".data"
)

// These are vars (not consts) so tests can shrink them to exercise rotation and
// reset without writing gigabytes.
var (
	// maxPackBytes rotates to a fresh pack once the active one exceeds this,
	// keeping individual files manageable for the OS and for backups.
	maxPackBytes int64 = 1 << 30 // 1 GiB
	// packResetBytes bounds unbounded cross-build growth: if the packs total
	// more than this at startup, whole packs are evicted OLDEST-FIRST down to
	// ~80% of this budget (the newest pack is never evicted) — see
	// evictPacksToBudget. Evicted records are recomputed on demand.
	packResetBytes int64 = 8 << 30 // 8 GiB
)

var packCRC = crc32.MakeTable(crc32.IEEE)

// maxInt is the largest value representable by int on this platform, used to
// bound a body length before converting int64 -> int for make().
const maxInt = int(^uint(0) >> 1)

// mmapVerifyThreshold is the body size at or above which CRC verification maps
// the pack region (via go-mmap) instead of reading the whole body onto the
// heap. Large bodies are compiled archives/export data; copying them per hit
// would be serious allocation pressure under parallel builds. Tiny entries (the
// common case) aren't worth the mmap/munmap syscalls and take a plain read.
const mmapVerifyThreshold = 1 << 16 // 64 KiB

// OpenPackStore opens (or creates) a pack store rooted at dir, rebuilding the
// in-memory index by scanning every pack file.
func OpenPackStore(dir string) (*PackStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("pack store: %w", err)
	}
	s := &PackStore{
		dir:      dir,
		byAction: make(map[string]packLoc),
		byOutput: make(map[string]packLoc),
		packs:    make(map[int]*os.File),
	}

	ids, total, err := s.discoverPacks()
	if err != nil {
		return nil, err
	}

	// Bound cross-build growth: evict oldest packs (never the newest) until
	// back under budget — see evictPacksToBudget. Evicted records are simply
	// recomputed; the hot tail of the cache survives.
	ids = s.evictPacksToBudget(ids, total)

	for _, id := range ids {
		f, err := os.OpenFile(s.packPath(id), os.O_RDWR, 0o644)
		if err != nil {
			return nil, fmt.Errorf("pack store open %d: %w", id, err)
		}
		s.packs[id] = f
		size, err := s.scanPack(id, f)
		if err != nil {
			s.Close()
			return nil, err
		}
		if id > s.activeID {
			s.activeID = id
			s.activeSize = size
		}
	}

	// Ensure there is an active pack to append to.
	if s.activeID == 0 {
		if err := s.openActive(1); err != nil {
			s.Close()
			return nil, err
		}
	}
	return s, nil
}

// discoverPacks returns the sorted pack IDs present in dir and their total size.
func (s *PackStore) discoverPacks() (ids []int, total int64, err error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, 0, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		id, ok := parsePackName(e.Name())
		if !ok {
			continue
		}
		ids = append(ids, id)
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	sort.Ints(ids)
	return ids, total, nil
}

// openActive creates pack file id and makes it the active (append) target.
func (s *PackStore) openActive(id int) error {
	f, err := os.OpenFile(s.packPath(id), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("pack store create %d: %w", id, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	s.pmu.Lock()
	s.packs[id] = f
	s.pmu.Unlock()
	s.activeID = id
	s.activeSize = info.Size()
	return nil
}

// scanPack rebuilds the index from a single pack file and returns its size.
// Scanning reads only the fixed-size record headers (never the bodies), so it
// is cheap even for multi-gigabyte packs. It stops at the first torn or corrupt
// record, treating everything after as absent.
func (s *PackStore) scanPack(id int, f *os.File) (int64, error) {
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	hdr := make([]byte, packHeaderLen)
	var off int64
	for off+packHeaderLen <= size {
		if _, err := f.ReadAt(hdr, off); err != nil {
			break
		}
		magic := binary.LittleEndian.Uint32(hdr[0:4])
		if magic != packRecordMagic && magic != packAliasMagic {
			break // garbage or torn write
		}
		actionID := hex.EncodeToString(hdr[4 : 4+hashLen])
		outputID := hex.EncodeToString(hdr[4+hashLen : 4+2*hashLen])
		created := int64(binary.LittleEndian.Uint64(hdr[4+2*hashLen : 12+2*hashLen]))
		dataLen := int64(binary.LittleEndian.Uint64(hdr[12+2*hashLen : 20+2*hashLen]))

		if magic == packAliasMagic {
			// Aliases carry no body; dataLen must be 0. A non-zero value means
			// corruption — stop rather than advance by only the header and risk
			// desyncing from the true record boundaries.
			if dataLen != 0 {
				break
			}
			// The body lives under outputID, written by an earlier full record
			// (records are scanned in write order). Point the action at it. If
			// the body record was lost/torn, the alias is orphaned — skip.
			if bodyLoc, ok := s.byOutput[outputID]; ok {
				loc := bodyLoc
				loc.created = created
				s.byAction[actionID] = loc
			}
			off += packHeaderLen // header only, no body
			continue
		}

		dataOff := off + packHeaderLen
		if dataLen < 0 || dataOff+dataLen > size {
			break // declared body runs past EOF: torn final record
		}
		crc := binary.LittleEndian.Uint32(hdr[20+2*hashLen : packHeaderLen])
		loc := packLoc{packID: id, dataOff: dataOff, dataLen: dataLen, created: created, outputID: outputID, crc: crc}
		// Last write wins for an action; content dedup keeps the first sighting.
		s.byAction[actionID] = loc
		if _, ok := s.byOutput[outputID]; !ok {
			s.byOutput[outputID] = loc
		}
		off = dataOff + dataLen
	}
	// Reclaim a stranded tail: bytes after the last valid record (a torn/garbage
	// append from a crash) are dead weight that inflates disk usage and the
	// reset accounting, and future appends would start past them. Truncate back
	// to the last good boundary. Best-effort — safe because the single-owner
	// lock means no other process is using this pack.
	if off < size {
		_ = f.Truncate(off)
	}
	return off, nil
}

// Get returns the location of actionID's body, incrementing the hit counter.
func (s *PackStore) Get(actionID string) (packLoc, bool) {
	s.mu.RLock()
	loc, ok := s.byAction[actionID]
	s.mu.RUnlock()
	if ok {
		s.Stats.Hits.Increment()
	}
	return loc, ok
}

// GetByOutput returns the location of a body by its outputID. Used by the FUSE
// layer to resolve a DiskPath (which is named by outputID) to its bytes. It
// does not count as a hit — the originating Get already did.
func (s *PackStore) GetByOutput(outputID string) (packLoc, bool) {
	s.mu.RLock()
	loc, ok := s.byOutput[outputID]
	s.mu.RUnlock()
	return loc, ok
}

// ReadAt reads up to len(dest) bytes of loc's body starting at body-relative
// offset off. It is safe for concurrent use (os.File.ReadAt is). A negative or
// past-end offset reads nothing — guarding it keeps a bad offset from
// underflowing into the record header or a neighbouring body.
func (s *PackStore) ReadAt(loc packLoc, dest []byte, off int64) (int, error) {
	if off < 0 || off >= loc.dataLen {
		return 0, io.EOF
	}
	want := loc.dataLen - off
	if int64(len(dest)) > want {
		dest = dest[:want]
	}
	f := s.pack(loc.packID)
	if f == nil {
		return 0, fmt.Errorf("pack read: pack %d missing", loc.packID)
	}
	return f.ReadAt(dest, loc.dataOff+off)
}

// fdForRead returns the pack file descriptor and absolute offset for serving a
// read of loc starting at body-relative off, plus how many bytes remain. It
// lets the FUSE layer hand the kernel a (fd, offset, size) so reads are served
// zero-copy (fuse.ReadResultFd) straight from the pack — no copy through the
// daemon. The fd stays valid for the store's lifetime; pread at an explicit
// offset is safe for concurrent use.
func (s *PackStore) fdForRead(loc packLoc, off int64) (fd uintptr, absOff, avail int64) {
	if off < 0 || off >= loc.dataLen {
		return 0, 0, 0
	}
	f := s.pack(loc.packID)
	if f == nil {
		return 0, 0, 0
	}
	return f.Fd(), loc.dataOff + off, loc.dataLen - off
}

// ReadAll returns the full body at loc.
func (s *PackStore) ReadAll(loc packLoc) ([]byte, error) {
	// dataLen comes from a scanned record (validated <= file size) or a Put
	// (len of an in-memory slice), so it's always a sane non-negative value;
	// guard anyway so a corrupt/overflowing length can't drive a bad make().
	if loc.dataLen < 0 || loc.dataLen > int64(maxInt) {
		return nil, fmt.Errorf("pack read: invalid body length %d", loc.dataLen)
	}
	buf := make([]byte, loc.dataLen)
	n, err := s.ReadAt(loc, buf, 0)
	if err != nil && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

// Close closes all open pack handles.
func (s *PackStore) Close() error {
	s.pmu.Lock()
	defer s.pmu.Unlock()
	var firstErr error
	for _, f := range s.packs {
		if err := f.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	s.packs = map[int]*os.File{}
	return firstErr
}

// Len reports how many distinct actions the store currently indexes.
func (s *PackStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.byAction)
}

func (s *PackStore) pack(id int) *os.File {
	s.pmu.RLock()
	defer s.pmu.RUnlock()
	return s.packs[id]
}

func (s *PackStore) packPath(id int) string {
	return filepath.Join(s.dir, fmt.Sprintf("%s%06d%s", packFilePrefix, id, packFileSuffix))
}

// parsePackName extracts the numeric id from "pack-000001.data".
func parsePackName(name string) (int, bool) {
	if !strings.HasPrefix(name, packFilePrefix) || !strings.HasSuffix(name, packFileSuffix) {
		return 0, false
	}
	mid := name[len(packFilePrefix) : len(name)-len(packFileSuffix)]
	if mid == "" {
		return 0, false
	}
	id := 0
	for i := 0; i < len(mid); i++ {
		if mid[i] < '0' || mid[i] > '9' {
			return 0, false
		}
		id = id*10 + int(mid[i]-'0')
	}
	return id, true
}

// decodeHash decodes a 64-char hex action/output ID into 32 raw bytes.
func decodeHash(hexID string) ([]byte, error) {
	if len(hexID) != hashLen*2 {
		return nil, fmt.Errorf("expected %d hex chars, got %d", hashLen*2, len(hexID))
	}
	b, err := hex.DecodeString(hexID)
	if err != nil {
		return nil, err
	}
	return b, nil
}
