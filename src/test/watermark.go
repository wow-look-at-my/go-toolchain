package test

import (
	"fmt"
	"strconv"
)

const watermarkAttr = "user.go-toolchain.watermark"

// GetWatermark reads the coverage watermark xattr from dir.
// Returns (value, exists, error).
func GetWatermark(dir string) (float32, bool, error) {
	data, exists, err := getXattr(dir, watermarkAttr)
	if err != nil {
		return 0, false, fmt.Errorf("reading watermark: %w", err)
	}
	if !exists {
		return 0, false, nil
	}

	val, err := strconv.ParseFloat(string(data), 32)
	if err != nil {
		return 0, false, fmt.Errorf("parsing watermark value %q: %w", string(data), err)
	}

	return float32(val), true, nil
}

// SetWatermark writes the coverage watermark xattr on dir.
func SetWatermark(dir string, coverage float32) error {
	data := []byte(strconv.FormatFloat(float64(coverage), 'f', 1, 32))
	if err := setXattr(dir, watermarkAttr, data); err != nil {
		return fmt.Errorf("writing watermark: %w", err)
	}
	return nil
}

// RemoveWatermark removes the coverage watermark xattr from dir.
func RemoveWatermark(dir string) error {
	if err := removeXattr(dir, watermarkAttr); err != nil {
		return fmt.Errorf("removing watermark: %w", err)
	}
	return nil
}
