package cmd

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// generateDirective represents a single //go:generate directive
type generateDirective struct {
	File    string // path to the .go file containing the directive
	Line    int    // line number of the directive
	Command string // the command to execute (after "//go:generate ")
}

// runGenerate executes all //go:generate directives with clean output handling.
// Output is captured, prefixed, and printed to stdout on success or stderr on failure.
// If quiet is true, output is suppressed on success.
// If expectedHash is empty, directives are shown but not executed (security prompt).
// If expectedHash matches the computed hash, directives are executed.
func runGenerate(quiet bool, expectedHash string) error {
	directives, err := findGenerateDirectives(".")
	if err != nil {
		return fmt.Errorf("failed to find generate directives: %w", err)
	}

	if len(directives) == 0 {
		return nil
	}

	// Compute hash of all directives
	hash := computeDirectivesHash(directives)

	// Allow explicit skip
	if expectedHash == "skip" {
		if !quiet {
			logger.Info("%s    Generate commands skipped%s", colorYellow, colorReset)
		}
		return nil
	}

	// If no hash provided or hash mismatch, show commands and stop
	if expectedHash == "" || expectedHash != hash {
		if !quiet {
			logger.Info("%s    Generate commands detected (not executed):%s", colorYellow, colorReset)
			for _, d := range directives {
				logger.Info("\t%s:%d: %s%s%s", d.File, d.Line, colorYellow, d.Command, colorReset)
			}
			logger.Info("\n%sTo run these commands, add: --generate %s%s", colorYellow, hash, colorReset)
		}
		return fmt.Errorf("generate commands require approval: --generate %s", hash)
	}

	// Hash matches, execute directives
	for _, d := range directives {
		if err := executeDirective(d, quiet); err != nil {
			return err
		}
	}

	return nil
}

// computeDirectivesHash computes a stable hash of all generate directives.
// The hash includes file paths, line numbers, and commands to detect any changes.
func computeDirectivesHash(directives []generateDirective) string {
	// Sort directives for stable ordering
	sorted := make([]generateDirective, len(directives))
	copy(sorted, directives)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].File != sorted[j].File {
			return sorted[i].File < sorted[j].File
		}
		return sorted[i].Line < sorted[j].Line
	})

	h := sha256.New()
	for _, d := range sorted {
		fmt.Fprintf(h, "%s:%d:%s\n", d.File, d.Line, d.Command)
	}

	// Return first 12 hex chars (48 bits) - enough to be unique, short enough to type
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// findGenerateDirectives walks the directory tree and extracts all //go:generate directives
func findGenerateDirectives(root string) ([]generateDirective, error) {
	var directives []generateDirective

	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Skip vendor directories
			if d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		fileDirectives, err := parseDirectives(path)
		if err != nil {
			return fmt.Errorf("failed to parse %s: %w", path, err)
		}
		directives = append(directives, fileDirectives...)
		return nil
	})

	return directives, err
}

var generateRegex = regexp.MustCompile(`^//go:generate\s+(.+)$`)

// parseDirectives extracts //go:generate directives from a single file.
// Uses bufio.Reader.ReadLine instead of Scanner to handle arbitrarily long lines
// (e.g., generated code with huge string literals) without buffering them entirely.
func parseDirectives(path string) ([]generateDirective, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var directives []generateDirective
	reader := bufio.NewReader(f)
	lineNum := 0

	for {
		lineNum++
		// ReadLine returns the start of a line. For lines longer than the
		// internal buffer, isPrefix is true and subsequent calls return the
		// rest. Generate directives are short, so the first chunk is always
		// enough to detect and extract them.
		chunk, isPrefix, err := reader.ReadLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		matches := generateRegex.FindSubmatch(chunk)
		if matches != nil {
			directives = append(directives, generateDirective{
				File:    path,
				Line:    lineNum,
				Command: string(matches[1]),
			})
		}

		// Discard remaining chunks of long lines without buffering
		for isPrefix {
			_, isPrefix, err = reader.ReadLine()
			if err != nil {
				if err == io.EOF {
					return directives, nil
				}
				return nil, err
			}
		}
	}

	return directives, nil
}

// executeDirective runs a single generate directive
func executeDirective(d generateDirective, quiet bool) error {
	dir := filepath.Dir(d.File)

	// Print the command being executed
	if !quiet {
		logger.Info("\t%s", d.Command)
	}

	// Set up environment like go generate does
	env := os.Environ()
	env = append(env,
		"GOFILE="+filepath.Base(d.File),
		fmt.Sprintf("GOLINE=%d", d.Line),
		"GOPACKAGE="+guessPackage(d.File),
	)

	// Use bash with strict mode to handle pipes, redirects, etc.
	cmd := exec.Command("bash", "-euo", "pipefail", "-c", d.Command)
	cmd.Dir = dir
	cmd.Env = env

	// Capture stdout and stderr together to preserve chronological order
	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	err := cmd.Run()
	output := combined.String()

	// Prefix each line with "> "
	prefixed := prefixOutput(output)

	if err != nil {
		// On failure, re-print the command and all output to stderr
		logger.Error("\t%s", d.Command)
		if prefixed != "" {
			logger.Output("%s", prefixed)
		}
		return fmt.Errorf("generate failed in %s:%d: %w", d.File, d.Line, err)
	}

	// On success, print output to stdout
	if !quiet && prefixed != "" {
		logger.Output("%s", prefixed)
	}

	return nil
}

// prefixOutput prefixes each line with "\t> "
func prefixOutput(output string) string {
	if output == "" {
		return ""
	}

	var result strings.Builder
	lines := strings.Split(output, "\n")

	for i, line := range lines {
		// Don't add a trailing newline if the original didn't have one
		if i == len(lines)-1 && line == "" {
			break
		}
		result.WriteString("\t> ")
		result.WriteString(line)
		result.WriteByte('\n')
	}

	return result.String()
}

// guessPackage attempts to determine the package name from a file path.
// This is a simple heuristic - we use the directory name.
func guessPackage(path string) string {
	dir := filepath.Dir(path)
	if dir == "." {
		// Try to get from go.mod or current directory name
		cwd, err := os.Getwd()
		if err == nil {
			return filepath.Base(cwd)
		}
		return "main"
	}
	return filepath.Base(dir)
}
