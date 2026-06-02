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
//	crc32     uint32  (IEEE crc of the body, for offline integrity checks)
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
}

const (
	// packRecordMagic ("GTPR") marks the start of every record. A scan that
	// reads anything else has hit garbage or a torn write and stops.
	packRecordMagic = 0x47545052
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
	// more than this at startup, the store is reset (a cold cache, not a
	// correctness problem). Mirrors how the server purges on a version bump.
	packResetBytes int64 = 8 << 30 // 8 GiB
)

var packCRC = crc32.MakeTable(crc32.IEEE)

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

	// Bound cross-build growth: a too-large store is reset rather than carried
	// forever. A reset just means a cold cache on the next build.
	if total > packResetBytes {
		for _, id := range ids {
			os.Remove(s.packPath(id))
		}
		ids = nil
	}

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
		if binary.LittleEndian.Uint32(hdr[0:4]) != packRecordMagic {
			break // garbage or torn write
		}
		actionID := hex.EncodeToString(hdr[4 : 4+hashLen])
		outputID := hex.EncodeToString(hdr[4+hashLen : 4+2*hashLen])
		created := int64(binary.LittleEndian.Uint64(hdr[4+2*hashLen : 12+2*hashLen]))
		dataLen := int64(binary.LittleEndian.Uint64(hdr[12+2*hashLen : 20+2*hashLen]))
		dataOff := off + packHeaderLen
		if dataLen < 0 || dataOff+dataLen > size {
			break // declared body runs past EOF: torn final record
		}
		loc := packLoc{packID: id, dataOff: dataOff, dataLen: dataLen, created: created, outputID: outputID}
		// Last write wins for an action; content dedup keeps the first sighting.
		s.byAction[actionID] = loc
		if _, ok := s.byOutput[outputID]; !ok {
			s.byOutput[outputID] = loc
		}
		off = dataOff + dataLen
	}
	return off, nil
}

// Put stores body under actionID/outputID and returns its location. Identical
// content (same outputID) already present is not re-appended — only the action
// mapping is added, giving content-addressed dedup for free.
func (s *PackStore) Put(actionID, outputID string, body io.Reader) (packLoc, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return packLoc{}, fmt.Errorf("pack put read: %w", err)
	}
	aid, err := decodeHash(actionID)
	if err != nil {
		return packLoc{}, fmt.Errorf("pack put action id: %w", err)
	}
	oid, err := decodeHash(outputID)
	if err != nil {
		return packLoc{}, fmt.Errorf("pack put output id: %w", err)
	}

	// Content dedup: if we already hold these exact bytes, reuse them.
	s.mu.RLock()
	existing, ok := s.byOutput[outputID]
	s.mu.RUnlock()
	if ok && existing.dataLen == int64(len(data)) {
		loc := existing
		loc.created = packNow()
		s.mu.Lock()
		s.byAction[actionID] = loc
		s.mu.Unlock()
		s.Stats.Puts.Increment()
		return loc, nil
	}

	created := packNow()
	rec := make([]byte, packHeaderLen+len(data))
	binary.LittleEndian.PutUint32(rec[0:4], packRecordMagic)
	copy(rec[4:4+hashLen], aid)
	copy(rec[4+hashLen:4+2*hashLen], oid)
	binary.LittleEndian.PutUint64(rec[4+2*hashLen:12+2*hashLen], uint64(created))
	binary.LittleEndian.PutUint64(rec[12+2*hashLen:20+2*hashLen], uint64(len(data)))
	binary.LittleEndian.PutUint32(rec[20+2*hashLen:packHeaderLen], crc32.Checksum(data, packCRC))
	copy(rec[packHeaderLen:], data)

	s.wmu.Lock()
	id := s.activeID
	off := s.activeSize
	f := s.pack(id)
	if f == nil {
		s.wmu.Unlock()
		return packLoc{}, fmt.Errorf("pack put: active pack %d missing", id)
	}
	if _, err := f.WriteAt(rec, off); err != nil {
		s.wmu.Unlock()
		return packLoc{}, fmt.Errorf("pack put write: %w", err)
	}
	s.activeSize = off + int64(len(rec))
	rotate := s.activeSize >= maxPackBytes
	s.wmu.Unlock()

	loc := packLoc{packID: id, dataOff: off + packHeaderLen, dataLen: int64(len(data)), created: created, outputID: outputID}
	s.mu.Lock()
	s.byAction[actionID] = loc
	s.byOutput[outputID] = loc
	s.mu.Unlock()
	s.Stats.Puts.Increment()

	if rotate {
		s.wmu.Lock()
		if s.activeID == id { // not already rotated by another goroutine
			_ = s.openActive(id + 1)
		}
		s.wmu.Unlock()
	}
	return loc, nil
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
// offset off. It is safe for concurrent use (os.File.ReadAt is).
func (s *PackStore) ReadAt(loc packLoc, dest []byte, off int64) (int, error) {
	if off >= loc.dataLen {
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

// ReadAll returns the full body at loc.
func (s *PackStore) ReadAll(loc packLoc) ([]byte, error) {
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
