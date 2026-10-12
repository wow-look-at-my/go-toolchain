// Package releasestamp writes the buildhost release number into a build output
// that reserves a slot for it. It imports nothing outside the standard library,
// so the action can build it from its own checkout with `go run`.
package releasestamp

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// releaseStampMagic opens the slot a binary reserves for its buildhost release number.
var releaseStampMagic = append([]byte("\x00GOTOOLCH"), []byte("AIN-REL")...)

const releaseStampPayload = 64

var releaseStampSlotLen = len(releaseStampMagic) + 1 + releaseStampPayload

var (
	errStampSlotTruncated = errors.New("the release stamp slot runs past the end of the file")
	errStampVersionEmpty  = errors.New("the release number is empty")
)

// findStampSlots returns the offset of every release stamp slot in data.
func findStampSlots(data []byte) []int {
	var hits []int
	for from := 0; ; {
		i := bytes.Index(data[from:], releaseStampMagic)
		if i < 0 {
			return hits
		}
		hits = append(hits, from+i)
		from += i + 1
	}
}

// stampRelease writes version into the slot in data and reports whether data
// had a slot. A binary with several slots is refused: the running program
// reads one of them, and nothing can tell which.
func stampRelease(data []byte, version string) (bool, error) {
	if version == "" {
		return false, errStampVersionEmpty
	}
	if len(version) > releaseStampPayload {
		return false, fmt.Errorf("the release number %q is %d bytes, the slot holds %d", version, len(version), releaseStampPayload)
	}
	hits := findStampSlots(data)
	switch len(hits) {
	case 0:
		return false, nil
	case 1:
	default:
		return false, fmt.Errorf("%d release stamp slots, want 1", len(hits))
	}
	at := hits[0]
	if at+releaseStampSlotLen > len(data) {
		return false, errStampSlotTruncated
	}
	payload := data[at+len(releaseStampMagic)+1 : at+releaseStampSlotLen]
	clear(payload)
	copy(payload, version)
	data[at+len(releaseStampMagic)] = byte(len(version))
	return true, nil
}

// readReleaseStamp reads back what the slot holds, the way the program reads it.
func readReleaseStamp(data []byte) (string, error) {
	hits := findStampSlots(data)
	if len(hits) != 1 {
		return "", fmt.Errorf("%d release stamp slots, want 1", len(hits))
	}
	at := hits[0]
	if at+releaseStampSlotLen > len(data) {
		return "", errStampSlotTruncated
	}
	n := int(data[at+len(releaseStampMagic)])
	if n == 0 || n > releaseStampPayload {
		return "", fmt.Errorf("the release stamp slot holds no number")
	}
	start := at + len(releaseStampMagic) + 1
	return string(data[start : start+n]), nil
}

// stampableOutput reports whether a build-directory entry is a build output the
// stamp applies to. The listing and the manifest describe outputs and are not ones.
func stampableOutput(name string) bool {
	switch {
	case name == "checksums.txt", name == "buildhost-artifacts.json", strings.HasSuffix(name, ".zip"):
		return false
	}
	return true
}

// StampDir stamps every build output in dir that declares a slot, reads each
// back, and rewrites its checksums.txt line over the stamped bytes. It returns
// the names of the outputs it stamped.
func StampDir(dir, version string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var stamped []string
	for _, e := range entries {
		if !e.Type().IsRegular() || !stampableOutput(e.Name()) {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		ok, err := stampRelease(data, version)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Name(), err)
		}
		if !ok {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, data, info.Mode().Perm()); err != nil {
			return nil, err
		}
		back, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		got, err := readReleaseStamp(back)
		if err != nil {
			return nil, fmt.Errorf("%s: reading the stamp back: %w", e.Name(), err)
		}
		if got != version {
			return nil, fmt.Errorf("%s: wrote release %q, read back %q", e.Name(), version, got)
		}
		stamped = append(stamped, e.Name())
	}
	if len(stamped) > 0 {
		if err := rehashChecksums(dir, stamped); err != nil {
			return nil, err
		}
	}
	return stamped, nil
}

// rehashChecksums rewrites the checksums.txt line of each named output with
// the hash of its bytes now. A directory with no checksums.txt has nothing to rewrite.
func rehashChecksums(dir string, names []string) error {
	path := filepath.Join(dir, "checksums.txt")
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	f.Close()
	if err := sc.Err(); err != nil {
		return err
	}
	for i, line := range lines {
		_, name, ok := strings.Cut(line, "  ")
		if !ok {
			continue
		}
		for _, n := range names {
			if n != name {
				continue
			}
			sum, err := fileSHA256(filepath.Join(dir, name))
			if err != nil {
				return err
			}
			lines[i] = sum + "  " + name
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
