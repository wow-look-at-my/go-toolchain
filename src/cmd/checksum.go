package cmd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// fileHash returns the hex-encoded sha256 digest of the file at path.
func fileHash(path string) (string, error) {
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

// generateChecksums creates a checksums.txt file in dir containing sha256
// digests of the given files. The format matches sha256sum output:
//
//	<hex-digest>  <filename>
//
// Lines are sorted lexicographically by filename. Returns the path to
// the generated checksums file.
func generateChecksums(dir string, files []string) (string, error) {
	if len(files) == 0 {
		return "", nil
	}

	type entry struct {
		name string
		hash string
	}

	var entries []entry
	for _, path := range files {
		h, err := fileHash(path)
		if err != nil {
			return "", fmt.Errorf("failed to hash %s: %w", path, err)
		}
		entries = append(entries, entry{name: filepath.Base(path), hash: h})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].name < entries[j].name
	})

	var sb strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&sb, "%s  %s\n", e.hash, e.name)
	}

	outPath := filepath.Join(dir, "checksums.txt")
	if err := os.WriteFile(outPath, []byte(sb.String()), 0644); err != nil {
		return "", fmt.Errorf("failed to write checksums.txt: %w", err)
	}

	logger.Info("  HASH checksums.txt")
	return outPath, nil
}
