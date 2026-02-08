//go:build windows

package test

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const watermarkStream = ":user.go-toolchain.watermark"

func GetWatermark(dir string) (float32, bool, error) {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return 0, false, err
	}
	streamPath := absDir + watermarkStream

	data, err := os.ReadFile(streamPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, false, nil
		}
		// ADS not found also shows up as path not found on some Windows versions
		if strings.Contains(err.Error(), "cannot find the file") || strings.Contains(err.Error(), "cannot find the path") {
			return 0, false, nil
		}
		return 0, false, err
	}

	val, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 32)
	if err != nil {
		return 0, false, err
	}

	return float32(val), true, nil
}

func SetWatermark(dir string, coverage float32) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	streamPath := absDir + watermarkStream

	data := []byte(strconv.FormatFloat(float64(coverage), 'f', 1, 32))
	return os.WriteFile(streamPath, data, 0644)
}

func RemoveWatermark(dir string) error {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	streamPath := absDir + watermarkStream

	err = os.Remove(streamPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		if strings.Contains(err.Error(), "cannot find the file") || strings.Contains(err.Error(), "cannot find the path") {
			return nil
		}
		return err
	}
	return nil
}
