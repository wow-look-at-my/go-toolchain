package test

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

const fileLengthAttr = "user.go-toolchain.filelength-ok"

// IsFileLengthExempt checks whether a file has a valid file-length exemption.
// The exemption is stored as an xattr containing "size:sha256hex".
// Returns true only if the xattr exists AND the current file matches.
func IsFileLengthExempt(path string) (bool, error) {
	data, exists, err := getXattr(path, fileLengthAttr)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, nil
	}

	parts := strings.SplitN(string(data), ":", 2)
	if len(parts) != 2 {
		return false, nil // malformed — treat as not exempt
	}

	savedSize, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false, nil
	}
	savedHash := parts[1]

	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	if info.Size() != savedSize {
		return false, nil // file size changed
	}

	hash, err := sha256File(path)
	if err != nil {
		return false, err
	}
	return hash == savedHash, nil
}

// ExemptFileLength marks a file as exempt from file-length checks.
// The exemption encodes the current file size and SHA-256 hash,
// so it auto-invalidates when the file changes.
func ExemptFileLength(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("exempting file length: %w", err)
	}

	hash, err := sha256File(path)
	if err != nil {
		return fmt.Errorf("exempting file length: %w", err)
	}

	value := fmt.Sprintf("%d:%s", info.Size(), hash)
	return setXattr(path, fileLengthAttr, []byte(value))
}

// RemoveFileLengthExemption removes the file-length exemption xattr from a file.
func RemoveFileLengthExemption(path string) error {
	return removeXattr(path, fileLengthAttr)
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
