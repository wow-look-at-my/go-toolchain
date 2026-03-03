//go:build windows

package test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// adsPath returns the NTFS Alternate Data Stream path for the given attribute.
func adsPath(filePath, attr string) (string, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return "", err
	}
	return abs + ":" + attr, nil
}

// getXattr reads the named extended attribute from path via NTFS ADS.
// Returns (data, exists, error).
func getXattr(path, attr string) ([]byte, bool, error) {
	stream, err := adsPath(path, attr)
	if err != nil {
		return nil, false, fmt.Errorf("reading xattr %s: %w", attr, err)
	}

	data, err := os.ReadFile(stream)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		if strings.Contains(err.Error(), "cannot find the file") || strings.Contains(err.Error(), "cannot find the path") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("reading xattr %s: %w", attr, err)
	}

	return data, true, nil
}

// setXattr writes the named extended attribute on path via NTFS ADS.
func setXattr(path, attr string, data []byte) error {
	stream, err := adsPath(path, attr)
	if err != nil {
		return fmt.Errorf("writing xattr %s: %w", attr, err)
	}

	if err := os.WriteFile(stream, data, 0644); err != nil {
		return fmt.Errorf("writing xattr %s: %w", attr, err)
	}
	return nil
}

// removeXattr removes the named extended attribute from path via NTFS ADS.
// Returns nil if the attribute does not exist.
func removeXattr(path, attr string) error {
	stream, err := adsPath(path, attr)
	if err != nil {
		return fmt.Errorf("removing xattr %s: %w", attr, err)
	}

	err = os.Remove(stream)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if strings.Contains(err.Error(), "cannot find the file") || strings.Contains(err.Error(), "cannot find the path") {
			return nil
		}
		return fmt.Errorf("removing xattr %s: %w", attr, err)
	}
	return nil
}
