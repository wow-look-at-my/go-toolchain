package logger

import (
	"fmt"
	"io"
)

// EmitGHAWarning writes a GitHub Actions ::warning workflow command.
// Per GHA docs, these must be written to stdout to be parsed by the runner.
// If file is non-empty, the annotation is associated with that file.
func EmitGHAWarning(w io.Writer, file, msg string) {
	if file != "" {
		fmt.Fprintf(w, "::warning file=%s::%s\n", file, msg)
	} else {
		fmt.Fprintf(w, "::warning ::%s\n", msg)
	}
}

// EmitGHAError writes a GitHub Actions ::error workflow command.
// Per GHA docs, these must be written to stdout to be parsed by the runner.
// If file is non-empty, the annotation is associated with that file.
func EmitGHAError(w io.Writer, file, msg string) {
	if file != "" {
		fmt.Fprintf(w, "::error file=%s::%s\n", file, msg)
	} else {
		fmt.Fprintf(w, "::error ::%s\n", msg)
	}
}
