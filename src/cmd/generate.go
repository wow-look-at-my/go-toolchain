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
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/hostos"
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
			logger.Info("%s", colorYellow+"    Generate commands skipped"+colorReset)
		}
		return nil
	}

	// If no hash provided or hash mismatch, show commands and stop
	if expectedHash == "" || expectedHash != hash {
		if !quiet {
			logger.Info("%s", colorYellow+"    Generate commands detected (not executed):"+colorReset)
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

	// Return the hex prefix below - enough to be unique, short enough to type
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
var shellGoFmtRegex = regexp.MustCompile(`(^|[;&|()\s])go\s+fmt($|[;&|()\s])`)

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
		// isPrefix means more chunks follow, but directives fit the leading chunk.
		chunk, isPrefix, err := reader.ReadLine()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		matches := generateRegex.FindSubmatch(chunk)
		if matches != nil {
			d := generateDirective{
				File:    path,
				Line:    lineNum,
				Command: string(matches[1]),
			}
			if isGoFmtGenerateCommand(d.Command) {
				return nil, fmt.Errorf("%s:%d: go fmt is not allowed in go:generate directives", d.File, d.Line)
			}
			directives = append(directives, d)
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

func isGoFmtGenerateCommand(command string) bool {
	args, err := splitGenerateCommand(command)
	if err != nil || len(args) == 0 {
		return false
	}
	if len(args) >= 2 && isGoCommand(args[0]) && args[1] == "fmt" {
		return true
	}
	if isShellCommand(args[0]) {
		for i := 1; i < len(args)-1; i++ {
			if args[i] == "-c" && shellGoFmtRegex.MatchString(args[i+1]) {
				return true
			}
		}
	}
	return false
}

func isGoCommand(command string) bool {
	base := filepath.Base(command)
	base = strings.TrimSuffix(strings.ToLower(base), ".exe")
	return base == "go"
}

func isShellCommand(command string) bool {
	switch filepath.Base(command) {
	case "sh", "bash":
		return true
	default:
		return false
	}
}

// executeDirective runs a single generate directive
func executeDirective(d generateDirective, quiet bool) error {
	dir := filepath.Dir(d.File)

	if !quiet {
		logger.Info("\t%s", d.Command)
	}

	// A directive's tool must RUN here, so it targets the host. Depth: docs/PIPELINE.md
	env := append(os.Environ(), "GOOS="+hostos.GOOS(), "GOARCH="+runtime.GOARCH)
	env = append(env,
		"GOFILE="+filepath.Base(d.File),
		fmt.Sprintf("GOLINE=%d", d.Line),
		"GOPACKAGE="+guessPackage(d.File),
	)

	expanded := expandGenerateVars(d.Command, d)

	args, err := splitGenerateCommand(expanded)
	if err != nil {
		return fmt.Errorf("generate directive in %s:%d: %w", d.File, d.Line, err)
	}
	if len(args) == 0 {
		return fmt.Errorf("generate directive in %s:%d: empty command", d.File, d.Line)
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = env

	// Stream live so the output watchdog sees it; quiet mode buffers instead.
	stream := &streamPrefixWriter{}
	var buffered bytes.Buffer
	if quiet {
		cmd.Stdout, cmd.Stderr = &buffered, &buffered
	} else {
		cmd.Stdout, cmd.Stderr = stream, stream
	}

	err = cmd.Run()
	stream.Flush()

	prefixed := prefixOutput(buffered.String())

	if err != nil {
		logger.Error("\t%s", d.Command)
		if prefixed != "" {
			logger.Error("%s", strings.TrimSuffix(prefixed, "\n"))
		}
		return fmt.Errorf("generate failed in %s:%d: %w", d.File, d.Line, err)
	}

	return nil
}

func splitGenerateCommand(line string) ([]string, error) {
	var words []string
	for {
		line = strings.TrimLeft(line, " \t")
		if line == "" {
			break
		}

		switch line[0] {
		case '"':
			i := 1
			for i < len(line) {
				if line[i] == '\\' && i+1 < len(line) {
					i += 2
					continue
				}
				if line[i] == '"' {
					break
				}
				i++
			}
			if i >= len(line) {
				return nil, fmt.Errorf("unterminated double-quoted string")
			}
			word, err := strconv.Unquote(line[:i+1])
			if err != nil {
				return nil, fmt.Errorf("bad quoted string: %w", err)
			}
			words = append(words, word)
			line = line[i+1:]

		case '`':
			i := strings.Index(line[1:], "`")
			if i < 0 {
				return nil, fmt.Errorf("unterminated raw string")
			}
			words = append(words, line[1:1+i])
			line = line[2+i:]

		default:
			i := strings.IndexAny(line, " \t")
			if i < 0 {
				i = len(line)
			}
			words = append(words, line[:i])
			line = line[i:]
		}
	}
	return words, nil
}

func expandGenerateVars(s string, d generateDirective) string {
	return os.Expand(s, func(key string) string {
		switch key {
		case "GOARCH":
			return runtime.GOARCH
		case "GOOS":
			return runtime.GOOS
		case "GOFILE":
			return filepath.Base(d.File)
		case "GOLINE":
			return fmt.Sprintf("%d", d.Line)
		case "GOPACKAGE":
			return guessPackage(d.File)
		case "GOROOT":
			return runtime.GOROOT()
		case "DOLLAR":
			return "$"
		default:
			return os.Getenv(key)
		}
	})
}

// prefixOutput prefixes each line with "\t> "
func prefixOutput(output string) string {
	if output == "" {
		return ""
	}

	var result strings.Builder
	lines := strings.Split(output, "\n")

	for i, line := range lines {
		// Don't add a trailing newline the original lacked
		if i == len(lines)-1 && line == "" {
			break
		}
		result.WriteString("\t> ")
		result.WriteString(line)
		result.WriteByte('\n')
	}

	return result.String()
}

// maxPendingLine bounds a pending, newline-less line (e.g. a \r progress bar).
const maxPendingLine = 32 << 10

// streamPrefixWriter is prefixOutput's live counterpart, used while a command runs.
type streamPrefixWriter struct {
	pending []byte
}

func (w *streamPrefixWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b == '\n' {
			w.emit()
			continue
		}
		w.pending = append(w.pending, b)
		if len(w.pending) >= maxPendingLine {
			w.emit()
		}
	}
	return len(p), nil
}

// Flush emits a trailing line the child left unterminated.
func (w *streamPrefixWriter) Flush() {
	if len(w.pending) > 0 {
		w.emit()
	}
}

func (w *streamPrefixWriter) emit() {
	logger.Info("\t> %s", strings.TrimSuffix(string(w.pending), "\r"))
	w.pending = w.pending[:0]
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
