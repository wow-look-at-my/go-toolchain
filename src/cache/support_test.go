package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"

	"github.com/wow-look-at-my/go-s3-server/cacheclient"
)

// These helpers moved out with the web-tier tests; the tiers that stayed still build real bodies under real keys.

// compressData frames a body the way the wire form is framed.
func compressData(data []byte) ([]byte, error) { return cacheclient.Compress(data) }

// testOutputID is the cache outputID for a body: its lowercase-hex sha256,
// which GETs verify against.
func testOutputID(data string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:])
}

// largePayload returns a payload of exactly n bytes, big enough that Put
// uploads it individually rather than batching it.
func largePayload(n int) string {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = byte('A' + i%26)
	}
	return string(buf)
}

func nopReader(s string) io.Reader {
	return strings.NewReader(s)
}

// primeIndex forces a key into the index, so a Get skips the batch path.
func primeIndex(b *WebBackend, actionID string) { b.MarkPresent(actionID) }

// archiveWithBuildID builds a minimal Go ar archive whose __.PKGDEF header
// carries `build id "<action>/<content>"`, the same shape `go build` stamps.
// Only the build id line matters to the guard, so the export data is a stub.
func archiveWithBuildID(action string) []byte {
	body := "go object linux amd64 go1.24.7\n" +
		"build id \"" + action + "/Cw9xV7fakecontentid\"\n\n\n" +
		"$$B\nu\x00\x00\x00\n$$\n"
	return buildAr("__.PKGDEF", []byte(body))
}

// buildAr writes a single-member ar archive, what a Go package ships in.
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

// newBareBackend is a remote-less backend: key grammar and guards, no transport.
func newBareBackend(prefix string) *WebBackend { return cacheclient.NewBareBackend(prefix) }
