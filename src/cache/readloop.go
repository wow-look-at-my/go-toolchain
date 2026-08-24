package cache

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

// GOCACHEPROG wire reader: newline-framed JSON; PUT body is quoted base64. No Scanner cap (see readProtoLine).

// badPutBodyError marks a malformed PUT body line; framing stayed aligned, so only this PUT fails (Err reply).
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

// expectedBodyLineLen returns an allocation-hint length for a PUT body line (4*ceil(n/3) base64 + quotes), capped against a bad size.
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

// readPutBody decodes the base64 PUT body line, skipping cmd/go's blank
// separator. Returns (body, nil) on success; (nil, *badPutBodyError) for a
// malformed line (stream stays aligned, replies Err, keeps serving); or
// (nil, io.EOF/read error) on a stream failure (caller stops serving).
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
			// No JSON escaping needed for base64, but accept any legal JSON string (compat with the old decoder).
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
