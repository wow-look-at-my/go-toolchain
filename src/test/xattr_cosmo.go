//go:build cosmo

package test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GOOS=cosmo (gosmopolitan) counts as `unix`, but the fork's stdlib syscall
// package exposes no {Get,Set,Remove}xattr wrappers and golang.org/x/sys/unix
// has no cosmo port — and a fat APE also lands on hosts (Windows via the
// embedded payload aside, e.g. some macOS setups) where native xattr support
// varies. So under cosmo the watermark is stored in a hidden sidecar file
// NEXT TO the target instead: for target /a/b the attribute `attr` lives at
// /a/.b.xattr.<sanitized attr>. Placing the sidecar in the target's PARENT
// keeps it out of the target directory itself — the watermark target is the
// module root, and a file inside it would show up in `git status` and trip
// the pipeline's dirty-tree checks. Get/set/remove semantics (including the
// "not found" path) mirror the windows ADS variant in xattr_windows.go.

// sidecarPath returns the sidecar file path holding attr for path.
func sidecarPath(path, attr string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir, base := filepath.Dir(abs), filepath.Base(abs)
	return filepath.Join(dir, "."+base+".xattr."+sanitizeAttr(attr)), nil
}

// sanitizeAttr makes an xattr name filename-safe: bytes outside
// [A-Za-z0-9._-] are %XX hex-escaped ('%' included), so distinct attribute
// names always map to distinct sidecar names.
func sanitizeAttr(attr string) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := 0; i < len(attr); i++ {
		c := attr[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&0xf])
		}
	}
	return b.String()
}

// getXattr reads the named extended attribute from path via its sidecar file.
// Returns (data, exists, error).
func getXattr(path, attr string) ([]byte, bool, error) {
	sidecar, err := sidecarPath(path, attr)
	if err != nil {
		return nil, false, fmt.Errorf("reading xattr %s: %w", attr, err)
	}

	data, err := os.ReadFile(sidecar)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading xattr %s: %w", attr, err)
	}
	return data, true, nil
}

// setXattr writes the named extended attribute on path via its sidecar file.
func setXattr(path, attr string, data []byte) error {
	sidecar, err := sidecarPath(path, attr)
	if err != nil {
		return fmt.Errorf("writing xattr %s: %w", attr, err)
	}
	if err := os.WriteFile(sidecar, data, 0644); err != nil {
		return fmt.Errorf("writing xattr %s: %w", attr, err)
	}
	return nil
}

// removeXattr removes the named extended attribute from path.
// Returns nil if the attribute does not exist.
func removeXattr(path, attr string) error {
	sidecar, err := sidecarPath(path, attr)
	if err != nil {
		return fmt.Errorf("removing xattr %s: %w", attr, err)
	}
	if err := os.Remove(sidecar); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("removing xattr %s: %w", attr, err)
	}
	return nil
}
