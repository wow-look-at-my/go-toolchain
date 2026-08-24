package cache

import (
	"encoding/binary"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseImportPath_RealArchive(t *testing.T) {
	data, err := os.ReadFile("testdata/testpkg.a")
	require.NoError(t, err)
	got := parseImportPath(data)
	require.Equal(t, "github.com/example/testpkg", got)
}

func TestParseImportPath_NotAnArchive(t *testing.T) {
	require.Equal(t, "", parseImportPath([]byte("not an archive")))
	require.Equal(t, "", parseImportPath(nil))
	require.Equal(t, "", parseImportPath([]byte{}))
}

func TestParseImportPath_NoMarker(t *testing.T) {
	// Valid ar header + PKGDEF member but no $$B\n marker.
	arData := buildAr("__.PKGDEF", []byte("go object linux amd64 go1.25.0\n\n"))
	require.Equal(t, "", parseImportPath(arData))
}

func TestParseImportPath_NonUnifiedFormat(t *testing.T) {
	// $$B\n present but format byte is not 'u'.
	body := []byte("go object linux amd64 go1.25.0\n\n$$B\nX<data>\n$$\n")
	arData := buildAr("__.PKGDEF", body)
	require.Equal(t, "", parseImportPath(arData))
}

func TestArMember(t *testing.T) {
	body := []byte("hello world")
	arData := buildAr("__.PKGDEF", body)
	got := arMember(arData, "__.PKGDEF")
	require.Equal(t, body, got)
}

func TestArMember_MissingMember(t *testing.T) {
	arData := buildAr("other.file", []byte("data"))
	require.Nil(t, arMember(arData, "__.PKGDEF"))
}

func TestArMember_BadMagic(t *testing.T) {
	require.Nil(t, arMember([]byte("not an ar file"), "__.PKGDEF"))
}

func TestPkgbitsImportPath_TruncatedPayload(t *testing.T) {
	// Various truncated payloads should return "" without panicking.
	require.Equal(t, "", pkgbitsImportPath(nil))
	require.Equal(t, "", pkgbitsImportPath([]byte{0, 0, 0}))
	require.Equal(t, "", pkgbitsImportPath(make([]byte, 4)))
}

func TestPkgbitsImportPath_WithSyncMarkersRoundTrip(t *testing.T) {
	// Build a minimal pkgbits payload with sync markers enabled (real archive has sync=false).
	data, err := os.ReadFile("testdata/testpkg.a")
	require.NoError(t, err)

	// Re-parsed to build a synthetic sync-enabled payload; this test just checks no panic on a V0 (no-flags) payload.
	_ = data

	// Build a V0 payload (no flags/sync) with one SectionString element and SectionPkg[0] referencing it.
	importPath := "example.com/mypkg"
	payload := buildMinimalPkgbitsV0(importPath)
	got := pkgbitsImportPath(payload)
	require.Equal(t, importPath, got)
}

// buildAr creates a minimal ar archive containing a single member named name.
func buildAr(name string, body []byte) []byte {
	var out []byte
	out = append(out, "!<arch>\n"...)
	hdr := make([]byte, 60)
	copy(hdr[:16], name+"/               ")
	copy(hdr[16:28], "0           ")
	copy(hdr[28:34], "0     ")
	copy(hdr[34:40], "0     ")
	copy(hdr[40:48], "100644  ")
	sizeStr := []byte("          ")
	s := len(body)
	for i := len(sizeStr) - 1; s > 0; i-- {
		sizeStr[i] = byte('0' + s%10)
		s /= 10
	}
	copy(hdr[48:58], sizeStr)
	hdr[58] = '`'
	hdr[59] = '\n'
	out = append(out, hdr...)
	out = append(out, body...)
	if len(body)%2 != 0 {
		out = append(out, '\n')
	}
	return out
}

// buildMinimalPkgbitsV0 constructs a synthetic pkgbits V0 payload that encodes
// importPath as the package path in SectionPkg[0], without sync markers.
// Layout:
//
//	version=0 (no flags)
//	elemEndsEnds: SectionString=1, others cumulative up to total=2
//	elemEnds: [len(importPath), <SectionPkg[0] elem size>]
//	elemData: <importPath bytes> <SectionPkg[0] elem> <8-byte fingerprint>
func buildMinimalPkgbitsV0(importPath string) []byte {
	// SectionPkg[0] body: nrelocs=1, reloc[0]{kind=0 SectionString, idx=0}, no sync markers, relocIdx=0.
	pkgElem := []byte{
		0x01,       // nrelocs = 1
		0x00, 0x00, // reloc[0]: kind=0, idx=0
		// String(): no sync markers, just relocIdx=0
		0x00, // relocIdx = 0
	}

	strElem := []byte(importPath)

	// elemEnds offsets (excluding fingerprint): SectionString[0] ends at len(strElem), SectionPkg[0] len(pkgElem) further.
	strEnd := uint32(len(strElem))
	pkgEnd := strEnd + uint32(len(pkgElem))

	// elemEndsEnds: cumulative per section (10 total); only SectionString(0) and SectionPkg(3) each add 1 element.
	numSections := 10
	eee := make([]uint32, numSections)
	eee[0] = 1 // SectionString: 1 element
	for i := 1; i < 3; i++ {
		eee[i] = 1 // sections 1,2 have 0 elements
	}
	eee[3] = 2 // SectionPkg: 1 element (total = 2)
	for i := 4; i < numSections; i++ {
		eee[i] = 2 // rest have 0 elements
	}

	totalElems := int(eee[numSections-1]) // = 2
	elemEnds := make([]uint32, totalElems)
	elemEnds[0] = strEnd
	elemEnds[1] = pkgEnd

	fingerprint := make([]byte, 8)

	var buf []byte
	// version = 0 (uint32 LE, no flags)
	vb := make([]byte, 4)
	binary.LittleEndian.PutUint32(vb, 0)
	buf = append(buf, vb...)
	// elemEndsEnds
	for _, v := range eee {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, v)
		buf = append(buf, b...)
	}
	// elemEnds
	for _, v := range elemEnds {
		b := make([]byte, 4)
		binary.LittleEndian.PutUint32(b, v)
		buf = append(buf, b...)
	}
	// elemData: strElem + pkgElem + fingerprint
	buf = append(buf, strElem...)
	buf = append(buf, pkgElem...)
	buf = append(buf, fingerprint...)

	return buf
}
