package bench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// diagnosticLineCap bounds a failure report. A panicking benchmark can print
// megabytes, and a wall of stack traces buries the one line that names the
// cause. Whatever is cut is counted and said out loud.
const diagnosticLineCap = 200

// diagnosticEvent is the part of a `go test -json` event a failure needs. A
// build failure carries ImportPath and no Package, which is why both are here.
type diagnosticEvent struct {
	Action     string `json:"Action"`
	Package    string `json:"Package"`
	ImportPath string `json:"ImportPath"`
	Output     string `json:"Output"`
}

// Diagnostics is what `go test` said about a failure: the compiler and linker
// errors, the FAIL lines, and whatever a failing benchmark printed.
//
// It exists because the stream is filtered on the way to the console — only
// benchmark result lines are shown — so a run that never produced a result
// showed the user nothing at all. A build that cannot link (a full disk is the
// case that found this) then reported as "benchmarks failed: exit status 1"
// with no benchmarks and no reason, which is a failure with the evidence
// removed.
//
// Build errors arrive as `build-output` events and everything else as `output`
// events, so both are read. The lines a passing run would print anyway are
// dropped: they are not evidence and they push the real error off the screen.
func Diagnostics(data []byte) string {
	var kept []string
	dropped := 0

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), 1<<20)
	for scanner.Scan() {
		var event diagnosticEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Action != "output" && event.Action != "build-output" {
			continue
		}
		for _, line := range strings.Split(strings.TrimRight(event.Output, "\n"), "\n") {
			if isRoutineBenchLine(line) {
				continue
			}
			if len(kept) < diagnosticLineCap {
				kept = append(kept, line)
			} else {
				dropped++
			}
		}
	}

	if len(kept) == 0 {
		return ""
	}
	out := strings.Join(kept, "\n")
	if dropped > 0 {
		out += fmt.Sprintf("\n... %d more lines not shown (of %d)", dropped, len(kept)+dropped)
	}
	return out
}

// isRoutineBenchLine reports whether a line is one a passing benchmark run
// prints anyway. A result line, the goos/goarch/pkg/cpu header, and the
// per-test progress lines say nothing about why a run failed.
func isRoutineBenchLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || trimmed == "PASS" {
		return true
	}
	if benchPattern.MatchString(trimmed) {
		return true
	}
	for _, prefix := range []string{"goos:", "goarch:", "pkg:", "cpu:", "=== RUN", "=== PAUSE", "=== CONT", "--- PASS", "ok  \t"} {
		if strings.HasPrefix(trimmed, prefix) || strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}
