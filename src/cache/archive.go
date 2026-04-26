package cache

import (
	"bytes"
	"encoding/binary"
)

// parseImportPath extracts the Go package import path from the binary export
// data embedded in a Go ar archive (the __.PKGDEF member). Returns "" if the
// data is not a recognised Go archive or has no decodable import path.
func parseImportPath(data []byte) string {
	pkgdef := arMember(data, "__.PKGDEF")
	if pkgdef == nil {
		return ""
	}

	// Find "$$B\n" which marks the start of the binary export section.
	// The byte immediately after is the format code: 'u' for unified IR (pkgbits).
	// Layout: $$B\n | u<pkgbits-payload> | \n$$\n
	marker := []byte("$$B\n")
	idx := bytes.Index(pkgdef, marker)
	if idx < 0 {
		return ""
	}
	after := pkgdef[idx+len(marker):]
	if len(after) == 0 || after[0] != 'u' {
		return "" // only unified IR (pkgbits) is supported
	}
	pkgbitsData := after[1:]
	// Strip the trailing end-of-section marker "\n$$\n" that follows the pkgbits payload.
	if end := bytes.Index(pkgbitsData, []byte("\n$$\n")); end >= 0 {
		pkgbitsData = pkgbitsData[:end]
	}
	return pkgbitsImportPath(pkgbitsData)
}

// arMember finds and returns the body of a named member in an ar archive.
func arMember(data []byte, name string) []byte {
	const globalHdr = "!<arch>\n"
	if len(data) < len(globalHdr) || string(data[:len(globalHdr)]) != globalHdr {
		return nil
	}
	data = data[len(globalHdr):]

	// Each member header is 60 bytes:
	//   name[16], mtime[12], uid[6], gid[6], mode[8], size[10], end[2]
	const hdrSize = 60
	for len(data) >= hdrSize {
		rawName := bytes.TrimRight(data[:16], " ")
		rawSize := bytes.TrimSpace(data[48:58])
		if string(data[58:60]) != "`\n" {
			return nil
		}
		size := 0
		for _, b := range rawSize {
			if b < '0' || b > '9' {
				return nil
			}
			size = size*10 + int(b-'0')
		}
		body := data[hdrSize:]
		if size > len(body) {
			return nil
		}
		// Strip BSD-style trailing slash from member name.
		memberName := string(bytes.TrimRight(rawName, "/"))
		if memberName == name {
			return body[:size]
		}
		// Members are padded to even byte boundaries.
		advance := hdrSize + size
		if size%2 != 0 {
			advance++
		}
		if advance > len(data) {
			return nil
		}
		data = data[advance:]
	}
	return nil
}

// pkgbits constants mirroring internal/pkgbits.
const (
	pbSectionString = 0
	pbSectionPkg    = 3
	pbNumSections   = 10

	pbFlagSyncMarkers = 1

	// Sync marker values (marker is stored in bits [63:8] of the varint).
	pbSyncRelocs   = 8
	pbSyncReloc    = 9
	pbSyncUseReloc = 10
	pbSyncUint64   = 4
	pbSyncString   = 5
	pbSyncPkgDef   = 17
)

// pkgbitsImportPath decodes the import path from a pkgbits payload (the bytes
// immediately after the "$B\n" marker in __.PKGDEF). Returns "" on any error.
func pkgbitsImportPath(payload []byte) string {
	if len(payload) < 4 {
		return ""
	}

	// Outer header: uint32 LE version.
	version := binary.LittleEndian.Uint32(payload[:4])
	payload = payload[4:]

	// V1+ adds uint32 LE flags.
	var flags uint32
	if version >= 1 {
		if len(payload) < 4 {
			return ""
		}
		flags = binary.LittleEndian.Uint32(payload[:4])
		payload = payload[4:]
	}
	sync := flags&pbFlagSyncMarkers != 0

	// [numSections]uint32 elemEndsEnds.
	const endsEndsBytes = pbNumSections * 4
	if len(payload) < endsEndsBytes {
		return ""
	}
	var elemEndsEnds [pbNumSections]uint32
	for i := range elemEndsEnds {
		elemEndsEnds[i] = binary.LittleEndian.Uint32(payload[i*4:])
	}
	payload = payload[endsEndsBytes:]

	// []uint32 elemEnds (total count = elemEndsEnds[numSections-1]).
	numElems := int(elemEndsEnds[pbNumSections-1])
	if len(payload) < numElems*4 {
		return ""
	}
	elemEnds := make([]uint32, numElems)
	for i := range elemEnds {
		elemEnds[i] = binary.LittleEndian.Uint32(payload[i*4:])
	}
	payload = payload[numElems*4:]

	// The remainder (minus the 8-byte fingerprint at the end) is elemData.
	const fingerprintSize = 8
	if len(payload) < fingerprintSize {
		return ""
	}
	elemData := payload[:len(payload)-fingerprintSize]

	// Helper: start byte offset of absolute element index e.
	elemStart := func(e int) uint32 {
		if e == 0 {
			return 0
		}
		return elemEnds[e-1]
	}

	// Helper: absolute index of section s's element 0.
	sectionBase := func(s int) int {
		if s == 0 {
			return 0
		}
		return int(elemEndsEnds[s-1])
	}

	// Validate that SectionPkg has at least one element.
	pkgBase := sectionBase(pbSectionPkg)
	pkgEnd := int(elemEndsEnds[pbSectionPkg])
	if pkgEnd <= pkgBase {
		return ""
	}

	// Get the raw bytes for SectionPkg[0].
	elem0 := pkgBase
	start := elemStart(elem0)
	end := elemEnds[elem0]
	if int(end) > len(elemData) || start > end {
		return ""
	}
	r := bytes.NewReader(elemData[start:end])

	// --- Parse the element header (reloc table) ---
	// Format (from NewDecoderRaw / TempDecoderRaw in internal/pkgbits):
	//   [sync(SyncRelocs)] [sync(SyncUint64)] uvarint:nrelocs
	//   for each reloc: [sync(SyncReloc)] [sync(SyncUint64)] uvarint:kind [sync(SyncUint64)] uvarint:idx

	if !skipSync(r, sync, pbSyncRelocs) {
		return ""
	}
	if !skipSync(r, sync, pbSyncUint64) {
		return ""
	}
	nrelocs, err := rawUvarint(r)
	if err != nil {
		return ""
	}

	type reloc struct{ kind, idx uint64 }
	relocs := make([]reloc, nrelocs)
	for i := range relocs {
		if !skipSync(r, sync, pbSyncReloc) {
			return ""
		}
		if !skipSync(r, sync, pbSyncUint64) {
			return ""
		}
		kind, err := rawUvarint(r)
		if err != nil {
			return ""
		}
		if !skipSync(r, sync, pbSyncUint64) {
			return ""
		}
		idx, err := rawUvarint(r)
		if err != nil {
			return ""
		}
		relocs[i] = reloc{kind, idx}
	}

	// --- Opening sync marker for this element (TempDecoder(SyncPkgDef)) ---
	if !skipSync(r, sync, pbSyncPkgDef) {
		return ""
	}

	// --- r.String() body ---
	// String() calls: Sync(SyncString), Reloc(SectionString)
	// Reloc() calls:  Sync(SyncUseReloc), Uint64()
	// Uint64() calls: Sync(SyncUint64), rawUvarint
	if !skipSync(r, sync, pbSyncString) {
		return ""
	}
	if !skipSync(r, sync, pbSyncUseReloc) {
		return ""
	}
	if !skipSync(r, sync, pbSyncUint64) {
		return ""
	}
	relocIdx, err := rawUvarint(r)
	if err != nil {
		return ""
	}
	if int(relocIdx) >= len(relocs) {
		return ""
	}
	ref := relocs[relocIdx]
	if ref.kind != pbSectionString {
		return ""
	}

	// --- Resolve SectionString[ref.idx] ---
	// String elements are stored as raw UTF-8 bytes (no header, no sync markers).
	strBase := sectionBase(pbSectionString)
	strEnd := int(elemEndsEnds[pbSectionString])
	strElemAbs := strBase + int(ref.idx)
	if strElemAbs >= strEnd {
		return ""
	}
	strStart := elemStart(strElemAbs)
	strEndOff := elemEnds[strElemAbs]
	if int(strEndOff) > len(elemData) || strStart > strEndOff {
		return ""
	}
	return string(elemData[strStart:strEndOff])
}

// skipSync optionally reads and verifies a pkgbits sync marker varint.
// When sync is false it is a no-op. Returns false on read error or mismatch.
func skipSync(r *bytes.Reader, sync bool, marker uint64) bool {
	if !sync {
		return true
	}
	v, err := rawUvarint(r)
	if err != nil {
		return false
	}
	return v>>8 == marker
}

// rawUvarint reads an unsigned varint (little-endian base-128).
func rawUvarint(r *bytes.Reader) (uint64, error) {
	var x uint64
	var s uint
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if b < 0x80 {
			return x | uint64(b)<<s, nil
		}
		x |= uint64(b&0x7f) << s
		s += 7
	}
}
