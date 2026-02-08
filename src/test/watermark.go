package test

import (
	"fmt"
	"strconv"

	"golang.org/x/sys/unix"
)

const watermarkAttr = "user.go-toolchain.watermark"

// getWatermark reads the coverage watermark xattr from dir.
// Returns (value, exists, error).
func GetWatermark(dir string) (float32, bool, error) {
	// First call with nil dest to get the size
	sz, err := unix.Getxattr(dir, watermarkAttr, nil)
	if err != nil {
		if isXattrNotFound(err) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("reading watermark: %w", err)
	}

	buf := make([]byte, sz)
	_, err = unix.Getxattr(dir, watermarkAttr, buf)
	if err != nil {
		return 0, false, fmt.Errorf("reading watermark: %w", err)
	}

	val, err := strconv.ParseFloat(string(buf), 32)
	if err != nil {
		return 0, false, fmt.Errorf("parsing watermark value %q: %w", string(buf), err)
	}

	return float32(val), true, nil
}

// setWatermark writes the coverage watermark xattr on dir.
func SetWatermark(dir string, coverage float32) error {
	data := []byte(strconv.FormatFloat(float64(coverage), 'f', 1, 32))
	if err := unix.Setxattr(dir, watermarkAttr, data, 0); err != nil {
		return fmt.Errorf("writing watermark: %w", err)
	}
	return nil
}

// removeWatermark removes the coverage watermark xattr from dir.
func RemoveWatermark(dir string) error {
	if err := unix.Removexattr(dir, watermarkAttr); err != nil {
		if isXattrNotFound(err) {
			return nil
		}
		return fmt.Errorf("removing watermark: %w", err)
	}
	return nil
}

// isXattrNotFound is defined in platform-specific files:
// watermark_darwin.go (ENOATTR) and watermark_linux.go (ENODATA).
