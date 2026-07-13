package logger

import (
	"fmt"
	"io"
	"strings"
)

// escapeGHAData escapes the message data of a GitHub Actions workflow command
// per the runner's encoding: % first (so the escape sequences introduced
// below are not themselves re-escaped), then CR and LF. Without this, a
// multi-line message is truncated to its first line in the annotation and
// the remaining lines leak into the log as plain text.
func escapeGHAData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

// escapeGHAProperty escapes a workflow-command property value (e.g. the
// file= property). Properties use the data encoding plus : and , which
// delimit properties in the command line.
func escapeGHAProperty(s string) string {
	s = escapeGHAData(s)
	s = strings.ReplaceAll(s, ":", "%3A")
	s = strings.ReplaceAll(s, ",", "%2C")
	return s
}

// emitGHA writes a GitHub Actions workflow command (::warning / ::error)
// with properly escaped properties and message data.
func emitGHA(w io.Writer, command, file, msg string) {
	if file != "" {
		fmt.Fprintf(w, "::%s file=%s::%s\n", command, escapeGHAProperty(file), escapeGHAData(msg))
	} else {
		fmt.Fprintf(w, "::%s ::%s\n", command, escapeGHAData(msg))
	}
}

// EmitGHAWarning writes a GitHub Actions ::warning workflow command.
// Per GHA docs, these must be written to stdout to be parsed by the runner.
// If file is non-empty, the annotation is associated with that file.
func EmitGHAWarning(w io.Writer, file, msg string) {
	emitGHA(w, "warning", file, msg)
}

// EmitGHAError writes a GitHub Actions ::error workflow command.
// Per GHA docs, these must be written to stdout to be parsed by the runner.
// If file is non-empty, the annotation is associated with that file.
func EmitGHAError(w io.Writer, file, msg string) {
	emitGHA(w, "error", file, msg)
}
