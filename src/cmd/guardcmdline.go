package cmd

import (
	"strings"

	agent "github.com/wow-look-at-my/is-this-an-agent"
)

// A reader outside our PID namespace cannot be named. The command line can.

// ancestryLimit bounds the walk against a cyclic ppid chain.
const ancestryLimit = 8

// A seam, so a test can drive the refused read that switched the guard off.
var readCmdlineFunc = readCmdline

// unidentifiedPeerSink answers for a pipe or socket whose reader this process
// cannot name. Convicting there aborts a bare `go-toolchain`.
func unidentifiedPeerSink(kind sinkKind) outputSink {
	cmd, piped, known := spawningPipeline()
	if piped {
		return outputSink{kind: kind, cmdline: cmd}
	}
	// An acquittal needs a shell that was READ and held no capture. An
	// unreadable argv, or an ancestry with no shell at all, shows nothing
	// either way -- and on darwin that is every `| cat`, whose reader is a
	// sibling the FIFO probe cannot reach.
	if !known {
		return outputSink{kind: kind, detail: "the reader could not be named and no ancestor's command line could be read"}
	}
	if cmd == "" {
		return outputSink{kind: kind, detail: "the reader could not be named and no shell ancestor was found to ask"}
	}
	return outputSink{kind: sinkVisible}
}

// spawningPipeline reports the shell text of the nearest ancestor handed a
// command string, and whether it captures stdout. known is false when this
// host refused an argv, which is not an ancestry that holds no shell.
func spawningPipeline() (cmdline string, piped, known bool) {
	lines, known := ancestorCmdlines()
	for _, argv := range lines {
		script, ok := shellScript(argv)
		if !ok {
			continue
		}
		return script, capturesStdout(script), known
	}
	return "", false, known
}

// ancestorCmdlines lists each ancestor's argv, starting at the parent. known
// is false when a pid resolved and its argv did not. A harness in another PID
// namespace leaves nothing to ask, which is an answer; a refused read is not.
func ancestorCmdlines() (lines [][]string, known bool) {
	known = true
	pid := parentPID()
	for i := 0; i < ancestryLimit && pid > 1; i++ {
		argv, ok := readCmdlineFunc(pid)
		switch {
		case ok && len(argv) > 0:
			lines = append(lines, argv)
		case !ok:
			known = false
		}
		next, ok := parentOf(pid)
		if !ok || next == pid {
			break
		}
		pid = next
	}
	return lines, known
}

// shellScript reports the command string a shell was handed with -c.
func shellScript(argv []string) (string, bool) {
	if len(argv) < 3 || !isShell(argv[0]) {
		return "", false
	}
	for i, a := range argv[1:] {
		if a == "-c" && i+2 < len(argv) {
			return argv[i+2], true
		}
		// A bundled form such as -lc still ends in the c that takes the string.
		if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.HasSuffix(a, "c") && i+2 < len(argv) {
			return argv[i+2], true
		}
	}
	return "", false
}

// isShell matches the interpreters that accept -c, by base name. A full path
// resolves, and so does a login dash.
func isShell(arg0 string) bool {
	base := arg0
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimPrefix(base, "-")
	base = strings.TrimSuffix(base, ".exe")
	switch base {
	case "sh", "bash", "dash", "zsh", "ksh", "ash", "busybox":
		return true
	}
	return false
}

// capturesStdout reports whether a shell script sends stdout somewhere other
// than the terminal. Quoted text is skipped, so `echo "a|b"` is not a pipe,
// and a redirect naming another descriptor is not a stdout redirect.
func capturesStdout(script string) bool {
	var quote byte
	for i := 0; i < len(script); i++ {
		c := script[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '\\':
			i++
		case '|':
			// `||` is control flow. It separates commands rather than
			// feeding the output of a command into the next.
			if i+1 < len(script) && script[i+1] == '|' {
				i++
				continue
			}
			return true
		case '`':
			return true
		case '$':
			if i+1 < len(script) && script[i+1] == '(' {
				return true
			}
		case '>':
			// A digit in front names a descriptor other than stdout.
			if i > 0 && script[i-1] >= '0' && script[i-1] <= '9' && script[i-1] != '1' {
				continue
			}
			return true
		}
	}
	return false
}

// parentPID is agent's view of our own parent, so the walk starts from the
// same ancestry the agent roster uses.
func parentPID() int {
	_, ppid, _ := agent.CommPPID(selfPID())
	return ppid
}

// parentOf is the same lookup for an arbitrary pid.
func parentOf(pid int) (int, bool) {
	_, ppid, ok := agent.CommPPID(pid)
	return ppid, ok
}
