//go:build unix

package test

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// getXattr reads the named extended attribute from path.
// Returns (data, exists, error).
func getXattr(path, attr string) ([]byte, bool, error) {
	sz, err := unix.Getxattr(path, attr, nil)
	if err != nil {
		if isXattrNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading xattr %s: %w", attr, err)
	}

	buf := make([]byte, sz)
	_, err = unix.Getxattr(path, attr, buf)
	if err != nil {
		return nil, false, fmt.Errorf("reading xattr %s: %w", attr, err)
	}

	return buf, true, nil
}

// setXattr writes the named extended attribute on path.
func setXattr(path, attr string, data []byte) error {
	if err := unix.Setxattr(path, attr, data, 0); err != nil {
		return fmt.Errorf("writing xattr %s: %w", attr, err)
	}
	return nil
}

// removeXattr removes the named extended attribute from path.
// Returns nil if the attribute does not exist.
func removeXattr(path, attr string) error {
	if err := unix.Removexattr(path, attr); err != nil {
		if isXattrNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing xattr %s: %w", attr, err)
	}
	return nil
}
