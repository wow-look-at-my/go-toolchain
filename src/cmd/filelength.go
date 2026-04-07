package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	fileLengthWarn  = 500
	fileLengthError = 750
)

// checkFileLength walks all .go files under root (excluding generated files)
// and warns at 500 lines, errors at 750 lines.
func checkFileLength(root string) error {
	var nWarn, nErr int

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		headerDone := false
		for scanner.Scan() {
			lineNum++
			// Check file header for generated marker before package clause
			if !headerDone {
				text := scanner.Text()
				if strings.Contains(text, "Code generated") {
					return nil
				}
				if strings.HasPrefix(text, "package ") {
					headerDone = true
				}
			}
		}

		if lineNum >= fileLengthError {
			logError(path, fmt.Sprintf("%s: %d lines (max %d)", path, lineNum, fileLengthError))
			nErr++
		} else if lineNum >= fileLengthWarn {
			logWarning(path, fmt.Sprintf("%s: %d lines (consider splitting, warning at %d)", path, lineNum, fileLengthWarn))
			nWarn++
		}
		return nil
	})

	if nWarn > 0 || nErr > 0 {
		fmt.Println()
	}

	if nErr > 0 {
		return fmt.Errorf("%d file(s) exceed %d lines", nErr, fileLengthError)
	}

	return nil
}
