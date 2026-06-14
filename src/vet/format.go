package vet

import (
	"bufio"
	"bytes"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var generatedCodeRe = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// RunGofmt checks that all Go source files in the current directory tree are
// formatted, routing every unformatted file through ed: a fix-mode editor
// rewrites it in place, a check-mode (CI) editor records it as a violation.
// Returns whether any file was written.
func RunGofmt(ed Editor) (bool, error) {
	var anyWrote bool

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
		if isGeneratedGoSource(src) {
			return nil
		}
		formatted, err := format.Source(src)
		if err != nil {
			// Parse errors are caught by go vet; skip unparse-able files here.
			return nil
		}
		if !bytes.Equal(src, formatted) {
			wrote, err := ed.Require(path, formatted, "not gofmt-formatted")
			if err != nil {
				return err
			}
			if wrote {
				anyWrote = true
			}
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("gofmt: %w", err)
	}

	return anyWrote, nil
}

func isGeneratedGoSource(src []byte) bool {
	scanner := bufio.NewScanner(bytes.NewReader(src))
	for scanner.Scan() {
		line := scanner.Text()
		if generatedCodeRe.MatchString(line) {
			return true
		}
		if strings.HasPrefix(line, "package ") {
			return false
		}
	}
	return false
}
