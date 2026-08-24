//go:build cosmo

package test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cosmo has no native xattr syscalls, so the watermark for /a/b lives in a
// sidecar file /a/.b.xattr.<attr> in the PARENT dir, keeping it out of the
// module-root target dir and `git status`. Semantics mirror xattr_windows.go.

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
