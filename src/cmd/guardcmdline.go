package cmd

import (
	"strings"

	agent "github.com/wow-look-at-my/is-this-an-agent"
)

// The pipe peer answers "who reads this", and a reader outside our PID
// namespace has no answer at all. The command line answers "what was typed",
// which is the question the guard is really asking, and it can be QUOTED back
// instead of described. This file is that source.

// ancestryLimit bounds the walk. A shell that ran us sits within a few hops,
// and a cycle in a synthetic ppid chain must not spin.
const ancestryLimit = 8

// unidentifiedPeerSink answers for a pipe or socket whose READER this process
// cannot name. It asks the command line instead, which is the only source
// that can state what the capture IS rather than assume a capture exists.
//
// A `| head` reader is a child of the same shell, so it is usually nameable
// and convicted before this. What is NOT nameable is a harness reading our
// stdout from outside our view -- another PID namespace, or a sandbox. Both
// classifiers used to convict there, which aborted a bare `go-toolchain` and
// told the caller to remove a pipe the command never had.
func unidentifiedPeerSink(kind sinkKind) outputSink {
	if cmd, piped := spawningPipeline(); piped {
		return outputSink{kind: kind, cmdline: cmd}
	}
	return outputSink{kind: sinkVisible}
}

// spawningPipeline reports the command line of the nearest ancestor that was
// handed a command STRING, and whether that string captures stdout. The
// string is the literal shell text, so a caller can print the pipeline the
// user actually typed.
//
// No shell ancestor means nothing typed a pipe on our behalf: a bare exec
// cannot introduce a pipe, so the answer is a confident "not piped".
func spawningPipeline() (string, bool) {
	for _, cmdline := range ancestorCmdlines() {
		script, ok := shellScript(cmdline)
		if !ok {
			continue
		}
		return script, capturesStdout(script)
	}
	return "", false
}

// ancestorCmdlines lists each ancestor's argv, in ancestry order, starting at
// the parent.
func ancestorCmdlines() [][]string {
	var out [][]string
	pid := parentPID()
	for i := 0; i < ancestryLimit && pid > 1; i++ {
		argv, ok := readCmdline(pid)
		if ok && len(argv) > 0 {
			out = append(out, argv)
		}
		next, ok := parentOf(pid)
		if !ok || next == pid {
			break
		}
		pid = next
	}
	return out
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
			// A digit in front names a descriptor, and only 1 is stdout.
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
