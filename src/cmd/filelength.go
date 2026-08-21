package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

const (
	fileLengthWarn  = 500
	fileLengthError = 750
)

// generatedFileRe matches the canonical "generated file" marker line. This is
// the same rule used by `go help generate`, gofmt, and golang.org/x/tools: a
// file is generated iff a line matching `^// Code generated .* DO NOT EDIT\.$`
// appears in the file header (before the package clause).
var generatedFileRe = regexp.MustCompile(`^// Code generated .* DO NOT EDIT\.$`)

// isGeneratedFile reports whether r is a generated Go source file per the
// canonical convention. It scans only the file header: leading `//go:build` /
// `// +build` constraints, ordinary `//` line comments, `/* ... */` block
// comments, and blank lines may precede the marker. Scanning stops at the first
// line that is non-blank, not a comment, and not a build constraint (normally
// the `package` clause), so a marker appearing after that point does not count.
func isGeneratedFile(r io.Reader) bool {
	scanner := bufio.NewScanner(r)
	// Allow long lines (generated files can have very long header lines).
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inBlockComment := false
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)

		// Inside a /* ... */ block comment: keep consuming until it closes.
		if inBlockComment {
			if idx := strings.Index(trimmed, "*/"); idx >= 0 {
				inBlockComment = false
			}
			continue
		}

		// Blank lines are part of the header.
		if trimmed == "" {
			continue
		}

		// The canonical marker, on a line by itself.
		if generatedFileRe.MatchString(trimmed) {
			return true
		}

		// Line comments and build constraints are header lines.
		if strings.HasPrefix(trimmed, "//") {
			continue
		}

		// Start of a block comment that may span multiple lines.
		if strings.HasPrefix(trimmed, "/*") {
			// A single-line /* ... */ closes on the same line.
			if !strings.Contains(trimmed[2:], "*/") {
				inBlockComment = true
			}
			continue
		}

		// First real (non-comment, non-blank) line — the header is over.
		return false
	}
	return false
}

// isGeneratedPath opens path and reports whether it is a generated file.
func isGeneratedPath(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	return isGeneratedFile(f)
}

// checkFileLength walks all .go files under root and warns at 500 lines, errors
// at 750 lines. Generated files (per isGeneratedFile) are skipped unless the
// --count-generated flag is set.
func checkFileLength(root string) error {
	var nWarn, nErr, nSkipped int

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name != "." && (strings.HasPrefix(name, ".") || name == "vendor" || name == "testdata" || name == "node_modules") {
				return filepath.SkipDir
			}
			// A nested module's files (e.g. src/compat/go-isatty) follow
			// their upstream's conventions, not this repo's length limits.
			if path != root && gomod.IsNestedModule(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Skip generated files unless asked to count them. Detection reopens
		// the file; line counting uses a second open below — simplest and
		// keeps each pass straightforward.
		if !countGenerated && isGeneratedPath(path) {
			nSkipped++
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
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
		logger.Info("")
	}

	if nSkipped > 0 {
		logger.Info("  File length check: skipped %d generated file(s)", nSkipped)
	}

	if nErr > 0 {
		return fmt.Errorf("%d file(s) exceed %d lines", nErr, fileLengthError)
	}

	return nil
}
