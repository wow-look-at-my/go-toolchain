package cache

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// This file implements the GOCACHEPROG wire reading for Server.Run: newline-
// framed JSON request lines, and for PUTs a base64 body line. It exists to
// replace the old bufio.Scanner loop, whose 64 MiB token cap killed the whole
// protocol loop on any PUT body >= ~48 MiB raw (base64 line >= 64 MiB) — cmd/go
// then failed the build with "GOCACHEPROG exited pre-Close". Worse, the body
// scan failed BEFORE handlePut ran, so an EMPTY body was committed under the
// request's real actionID/outputID and served as a "valid" hit forever after.
//
// Wire format per cmd/go/internal/cache/prog.go (verified against go1.25.0):
// each request is one JSON line followed by an extra '\n'; a PUT with
// BodySize > 0 then writes '"' + base64(body) + '"' + '\n'. Despite older
// comments in this repo, go1.25.0 still writes the QUOTED form; the raw
// (unquoted) base64 form is also accepted here for forward compatibility.

// badPutBodyError marks a malformed PUT body line. The framing (one full
// line) was consumed, so the protocol stream is still aligned: the caller
// fails only this PUT (Err reply, nothing stored) and keeps serving.
type badPutBodyError struct{ err error }

func (e *badPutBodyError) Error() string { return e.err.Error() }
func (e *badPutBodyError) Unwrap() error { return e.err }

// readProtoLine reads one newline-terminated line from br with no upper bound
// on line length (the old Scanner cap is exactly the bug this replaces). The
// returned slice may alias br's internal buffer and is only valid until the
// next read from br. A final line without a trailing newline is returned as-is
// (matching bufio.Scanner); a clean EOF returns (nil, io.EOF).
//
// hint is a capacity hint for lines that overflow br's buffer, so a huge
// base64 body line of known size lands in roughly one allocation.
func readProtoLine(br *bufio.Reader, hint int) ([]byte, error) {
	s, err := br.ReadSlice('\n')
	if err == nil {
		return s[:len(s)-1], nil
	}
	if err == io.EOF {
		if len(s) == 0 {
			return nil, io.EOF
		}
		return s, nil
	}
	if err != bufio.ErrBufferFull {
		return nil, err
	}
	// Line exceeds the reader's buffer: accumulate fragments.
	if hint < 2*len(s) {
		hint = 2 * len(s)
	}
	buf := make([]byte, 0, hint)
	buf = append(buf, s...)
	for {
		s, err = br.ReadSlice('\n')
		buf = append(buf, s...)
		switch err {
		case nil:
			return buf[:len(buf)-1], nil
		case bufio.ErrBufferFull:
			continue
		case io.EOF:
			return buf, nil
		default:
			return nil, err
		}
	}
}

// expectedBodyLineLen returns the expected length of a PUT body line for a
// declared BodySize: 4*ceil(n/3) base64 bytes plus the optional quotes. Used
// purely as an allocation hint (capped so a corrupt BodySize cannot drive a
// giant allocation); the line is still read to its actual newline.
func expectedBodyLineLen(bodySize int64) int {
	const maxHint = 64 << 20
	if bodySize <= 0 {
		return 0
	}
	l := 4*((bodySize+2)/3) + 3
	if l > maxHint {
		return maxHint
	}
	return int(l)
}

// readPutBody reads and decodes the base64 body line that follows a PUT
// request line, skipping the blank separator line cmd/go writes. It returns:
//
//   - (body, nil) on success, with len(body) verified == bodySize;
//   - (nil, *badPutBodyError) for a malformed line — bad base64, broken
//     quoting, or a decoded-size mismatch. The stream is still line-aligned,
//     so the caller replies Err for this PUT (storing NOTHING) and continues;
//   - (nil, io.EOF or a read error) for a stream-level failure. Nothing was
//     stored; the caller stops serving.
func readPutBody(br *bufio.Reader, bodySize int64) ([]byte, error) {
	for {
		line, err := readProtoLine(br, expectedBodyLineLen(bodySize))
		if err != nil {
			return nil, err
		}
		if len(line) == 0 {
			continue // the extra '\n' between the JSON line and the body line
		}
		body, derr := decodePutBody(line, bodySize)
		if derr != nil {
			return nil, &badPutBodyError{derr}
		}
		return body, nil
	}
}

// decodePutBody decodes one PUT body line. Both wire forms are supported:
// a JSON-quoted base64 string ('"'+base64+'"', what go <= 1.25 writes) and
// raw unquoted base64. The decoded length must equal the request's declared
// BodySize — a mismatch means a truncated or corrupt line, and committing it
// (or an empty body) under the request's real IDs would poison the cache.
func decodePutBody(line []byte, bodySize int64) ([]byte, error) {
	raw := line
	if raw[0] == '"' {
		if len(raw) < 2 || raw[len(raw)-1] != '"' {
			return nil, fmt.Errorf("unterminated quoted body line (%d bytes)", len(raw))
		}
		inner := raw[1 : len(raw)-1]
		if bytes.IndexByte(inner, '\\') >= 0 {
			// The base64 alphabet needs no JSON escaping, so cmd/go never
			// writes escapes — but accept any legal JSON string for
			// compatibility with the old json.Unmarshal-based decoder.
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return nil, fmt.Errorf("body line is not a valid JSON string: %w", err)
			}
			inner = []byte(s)
		}
		raw = inner
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(raw)))
	n, err := base64.StdEncoding.Decode(decoded, raw)
	if err != nil {
		return nil, fmt.Errorf("body base64 decode: %w", err)
	}
	if int64(n) != bodySize {
		return nil, fmt.Errorf("body size mismatch: decoded %d bytes, request declared %d", n, bodySize)
	}
	return decoded[:n], nil
}
