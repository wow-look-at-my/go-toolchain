package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

const (
	fileLengthWarn  = 500
	fileLengthError = 750
)

type fileLengthEntry struct {
	path  string
	lines int
}

// checkFileLength walks all .go files under root (excluding generated files)
// and warns at 500 lines, errors at 750 lines.
func checkFileLength(root string) error {
	var warnings []string
	var errors []string
	var warnFiles []fileLengthEntry
	var errFiles []fileLengthEntry

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
			exempt, _ := gotest.IsFileLengthExempt(path)
			if exempt {
				warnings = append(warnings, fmt.Sprintf("  %s: %d lines (exempt, warning at %d)", path, lineNum, fileLengthWarn))
				warnFiles = append(warnFiles, fileLengthEntry{path, lineNum})
			} else {
				errors = append(errors, fmt.Sprintf("  %s: %d lines (max %d)", path, lineNum, fileLengthError))
				errFiles = append(errFiles, fileLengthEntry{path, lineNum})
			}
		} else if lineNum >= fileLengthWarn {
			warnings = append(warnings, fmt.Sprintf("  %s: %d lines (consider splitting, warning at %d)", path, lineNum, fileLengthWarn))
			warnFiles = append(warnFiles, fileLengthEntry{path, lineNum})
		}
		return nil
	})

	gha := os.Getenv("GITHUB_ACTIONS") == "true"

	if len(warnings) > 0 {
		fmt.Printf("\n%slong files:%s\n", colorYellow, colorReset)
		for _, w := range warnings {
			fmt.Println(w)
		}
		if gha {
			for _, w := range warnFiles {
				fmt.Printf("::warning file=%s::%s: %d lines (consider splitting, warning at %d)\n", w.path, w.path, w.lines, fileLengthWarn)
			}
		}
		fmt.Println()
	}

	if len(errors) > 0 {
		fmt.Printf("\n%sfiles exceed maximum length:%s\n", colorRed, colorReset)
		for _, e := range errors {
			fmt.Println(e)
		}
		if gha {
			for _, e := range errFiles {
				fmt.Printf("::error file=%s::%s: %d lines (max %d)\n", e.path, e.path, e.lines, fileLengthError)
			}
		}
		fmt.Println()
		return fmt.Errorf("%d file(s) exceed %d lines", len(errors), fileLengthError)
	}

	return nil
}
