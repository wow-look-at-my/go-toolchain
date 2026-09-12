package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// slopfmtSkipDirs hold text nobody here authored.
var slopfmtSkipDirs = set.Of("vendor", "node_modules", "testdata")

// slopfmtMaxFileBytes is where a file stops being prose and becomes a blob.
const slopfmtMaxFileBytes = 1 << 20

// slopfmtArgBatch keeps a command line inside what every host accepts.
const slopfmtArgBatch = 256

// slopfixReads mirrors the extensions slopfix parses. It reads a named file
// whatever the extension, so the walk is what keeps an image out.
var slopfixReads = set.Of(".go", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh",
	".rs", ".sh", ".bash", ".zsh", ".js", ".jsx", ".mjs", ".cjs",
	".ts", ".mts", ".cts", ".tsx", ".yml", ".yaml", ".toml", ".conf")

// runSlopfmtPhase reports every number stated in a comment, anywhere in the
// tree. Nothing resolves an import or starts a compiler, so it must stay ahead
// of every other phase: that is what it buys. Warnings only, and the budget is
// what fails the build. Depth: docs/COMMENT-SCAN.md
func runSlopfmtPhase(root string) error {
	files := slopfmtFiles(root)
	if len(files) == 0 {
		return nil
	}
	st := logStep("comment scan")
	bin, err := ensureSlopfixFunc()
	if err != nil {
		st.done()
		return err
	}
	for _, batch := range batched(files, slopfmtArgBatch) {
		hits, err := slopfixFindings(bin, batch)
		if err != nil {
			st.done()
			return err
		}
		for _, hit := range hits {
			logger.WarnFile(hit.path, "%s:%d:%d: %q is a number in a comment: %s",
				hit.path, hit.line, hit.col, hit.number, slopfixRemedy)
		}
	}
	st.done()
	return nil
}

// slopfixRemedy is what a reader does about a finding.
const slopfixRemedy = "a number in a comment is a count of what exists today, " +
	"and the edit that adds an item leaves it wrong: describe what the code " +
	"does and let the reader count"

// slopfixHit is a reported number and where it sits.
type slopfixHit struct {
	path   string
	line   int
	col    int
	number string
}

// slopfixFindings runs the rule over a batch. A finding per line rather than
// per number, because the repair is a rewrite of the line whatever it counts.
func slopfixFindings(bin string, files []string) ([]slopfixHit, error) {
	out, err := runSlopfix(bin, files)
	if err != nil {
		return nil, err
	}
	seen := set.New[string]()
	var hits []slopfixHit
	scan := bufio.NewScanner(strings.NewReader(out))
	scan.Buffer(make([]byte, 0, 64*1024), slopfmtMaxFileBytes)
	for scan.Scan() {
		hit, ok := parseSlopfixLine(scan.Text())
		if !ok {
			continue
		}
		key := hit.path + ":" + strconv.Itoa(hit.line)
		if seen.Contains(key) {
			continue
		}
		seen.Add(key)
		hits = append(hits, hit)
	}
	return hits, nil
}

// runSlopfix reads the tool's stdout. A non-zero exit is how it reports a
// finding, so only a failure to START is an error. Swallowing it reports a
// clean tree for a scan that read nothing.
func runSlopfix(bin string, files []string) (string, error) {
	args := append([]string{"comments"}, files...)
	cmd := exec.Command(bin, args...)
	// A fat APE starts through a shell header execve never reads.
	if isAPE(bin) && runtime.GOOS != "windows" {
		cmd = exec.Command("/bin/sh", append([]string{bin}, args...)...)
	}
	out, err := cmd.Output()
	if err == nil || isExitError(err) {
		return string(out), nil
	}
	return "", fmt.Errorf("slopfix at %s did not start: %w", bin, err)
}

// isAPE reports whether the file opens on the header a shell has to read.
func isAPE(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var head [2]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return false
	}
	return head[0] == 'M' && head[1] == 'Z'
}

// isExitError reports whether the tool chose its own exit code. Go reports a
// signal death as the same type, and that tool read nothing.
func isExitError(err error) bool {
	var exit *exec.ExitError
	return errors.As(err, &exit) && exit.ProcessState.Exited()
}

// parseSlopfixLine reads a `path:line:col: "N" is a number in a comment`.
// A path holds a colon on NT, so the numbers are found from the right.
func parseSlopfixLine(line string) (slopfixHit, bool) {
	rest, quoted, found := strings.Cut(line, ` "`)
	if !found {
		return slopfixHit{}, false
	}
	number, _, found := strings.Cut(quoted, `"`)
	if !found {
		return slopfixHit{}, false
	}
	rest = strings.TrimSuffix(rest, ":")
	head, colText, found := cutLast(rest, ":")
	if !found {
		return slopfixHit{}, false
	}
	path, lineText, found := cutLast(head, ":")
	if !found {
		return slopfixHit{}, false
	}
	at, err := strconv.Atoi(lineText)
	if err != nil {
		return slopfixHit{}, false
	}
	col, err := strconv.Atoi(colText)
	if err != nil {
		return slopfixHit{}, false
	}
	return slopfixHit{path: path, line: at, col: col, number: number}, true
}

// cutLast splits around the final separator, never the leading separator.
func cutLast(s, sep string) (before, after string, found bool) {
	at := strings.LastIndex(s, sep)
	if at < 0 {
		return s, "", false
	}
	return s[:at], s[at+len(sep):], true
}

// batched hands out consecutive batches of at most n.
func batched(in []string, n int) [][]string {
	var out [][]string
	for len(in) > n {
		out = append(out, in[:n])
		in = in[n:]
	}
	if len(in) > 0 {
		out = append(out, in)
	}
	return out
}

// slopfmtFiles returns every file under root the rule reads.
func slopfmtFiles(root string) []string {
	// Where the root is not a module, the modules below it are the whole tree.
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	rootIsModule := err == nil
	var out []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if slopfmtSkipDir(root, path, d.Name(), rootIsModule) {
				return filepath.SkipDir
			}
			return nil
		}
		if !slopfixReads.Contains(strings.ToLower(filepath.Ext(path))) {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() > slopfmtMaxFileBytes {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out
}

// slopfmtSkipDir reports whether the walk stops at this directory.
func slopfmtSkipDir(root, path, name string, rootIsModule bool) bool {
	if path == root {
		return false
	}
	if strings.HasPrefix(name, ".") || name == outputDir || slopfmtSkipDirs.Contains(name) {
		return true
	}
	return rootIsModule && gomod.IsNestedModule(path)
}
