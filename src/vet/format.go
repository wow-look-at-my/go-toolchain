package vet

import (
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// RunGofmt checks that all Go source files in the current directory tree are
// formatted. If fix is true, unformatted files are rewritten in place and
// filesChanged=true is returned. If fix is false and unformatted files exist,
// an error listing them is returned.
func RunGofmt(fix bool) (bool, error) {
	var unformatted []string

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		formatted, err := format.Source(src)
		if err != nil {
			// Parse errors are caught by go vet; skip unparse-able files here.
			return nil
		}
		if !bytes.Equal(src, formatted) {
			if fix {
				if err := os.WriteFile(path, formatted, 0o644); err != nil {
					return err
				}
			}
			unformatted = append(unformatted, path)
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("gofmt: %w", err)
	}

	if len(unformatted) == 0 {
		return false, nil
	}
	if fix {
		return true, nil
	}

	var sb strings.Builder
	sb.WriteString("gofmt: unformatted files (run `go-toolchain` locally to fix):\n")
	for _, f := range unformatted {
		sb.WriteString("  " + f + "\n")
	}
	return false, fmt.Errorf("%s", strings.TrimRight(sb.String(), "\n"))
}
