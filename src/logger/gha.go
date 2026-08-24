package logger

import (
	"fmt"
	"io"
	"strings"
)

// escapeGHAData escapes % first (so escapes below aren't re-escaped), then
// CR/LF, so a multi-line message annotates intact.
func escapeGHAData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

// escapeGHAProperty escapes a property value (e.g. file=): the data encoding
// plus : and , since those delimit properties in the command line.
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

// EmitGHAWarning writes a ::warning command to w (stdout, for the runner).
// If file is non-empty, it names the annotated file.
func EmitGHAWarning(w io.Writer, file, msg string) {
	emitGHA(w, "warning", file, msg)
}

// EmitGHAError writes a ::error command to w (stdout, for the runner).
// If file is non-empty, it names the annotated file.
func EmitGHAError(w io.Writer, file, msg string) {
	emitGHA(w, "error", file, msg)
}
