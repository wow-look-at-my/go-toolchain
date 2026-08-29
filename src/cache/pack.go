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

// PackStore is a content-addressed store that appends bodies into a small
// number of pack files instead of a file per entry, avoiding inode/block waste.
// It backs FuseCache but stays FUSE-free for unit testing.
//
// Record: magic, actionID, outputID, created, dataLen, crc32 (widths in packHeaderLen)
// data. The index rebuilds by scanning packs at startup; a torn final record
// (crash mid-append) is detected by length past EOF and ignored.
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

	// verified caches per-record serve-gate facts to avoid re-hashing on repeat GETs (see verify.go).
	verified verifiedSet
}

// packLoc records where a body lives: pack file, byte offset past the header,
// and length. created/outputID travel with it so Get needs no disk access.
type packLoc struct {
	packID   int
	dataOff  int64
	dataLen  int64
	created  int64
	outputID string
	crc      uint32 // IEEE crc32 of the body, from the record header; verified on read
}

const (
	// packRecordMagic ("GTPR") starts a full record; anything else is garbage or a torn write.
	packRecordMagic = 0x47545052
	// packAliasMagic ("GTAL") aliases an actionID to an existing outputID's body, so dedup survives a restart.
	packAliasMagic = 0x4754414c
	// packHeaderLen is the header size: magic+actionID+outputID+created+dataLen+crc.
	packHeaderLen = 4 + 32 + 32 + 8 + 8 + 4
	// hashLen is the byte length of an action/output ID (a sha256 digest).
	hashLen = 32
	// packFilePrefix/Suffix name the pack files: pack-<id>.data
	packFilePrefix = "pack-"
	packFileSuffix = ".data"
)

// These are vars (not consts) so tests can shrink them to exercise rotation and
// reset without writing gigabytes.
var (
	// maxPackBytes rotates to a fresh pack as soon as the active pack exceeds this, keeping files manageable.
	maxPackBytes int64 = 1 << 30
	// packResetBytes caps total pack size; over it, oldest packs are evicted back under the target (see evictPacksToBudget).
	packResetBytes int64 = 8 << 30
)

var packCRC = crc32.MakeTable(crc32.IEEE)

// maxInt bounds a body length before converting int64 -> int for make().
const maxInt = int(^uint(0) >> 1)

// mmapVerifyThreshold: CRC-verify large bodies via mmap instead of a heap copy, to avoid allocation pressure.
const mmapVerifyThreshold = 1 << 16

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

	// Evict oldest packs (never newest) back under budget; evicted records are simply recomputed.
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
// is cheap even for multi-gigabyte packs. It stops at the earliest torn or corrupt
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
			// Aliases carry no body; a dataLen means corruption -- stop rather than desync record boundaries.
			if dataLen != 0 {
				break
			}
			// The body lives under outputID from an earlier record; point the action there. Orphaned (lost body) is skipped.
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
		// Last write wins for an action; content dedup keeps the earliest sighting.
		s.byAction[actionID] = loc
		if _, ok := s.byOutput[outputID]; !ok {
			s.byOutput[outputID] = loc
		}
		off = dataOff + dataLen
	}
	// Truncate a stranded tail (crash mid-append) back to the last good record; sole owner makes this safe.
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

// GetByOutput resolves a body by outputID, for FUSE DiskPath lookups. Not counted as a hit -- the originating Get did.
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

// fdForRead returns the pack fd and absolute offset for a body-relative read
// of loc, so FUSE can serve it without a copy; the fd stays valid for the store's life.
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
	// dataLen is normally sane (scanned + validated, or a slice len); guard anyway against a corrupt/overflowing make().
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

// parsePackName extracts the numeric id from a "pack-<id>.data" name.
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

// decodeHash decodes a hex action/output ID into its raw bytes (hashLen of them).
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
