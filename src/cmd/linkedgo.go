package cmd

import (
	"path/filepath"
	"strings"

	gocmd "cmd/go"
)

// LinkedGoArgs answers the go command line argv asks for, and whether it asks
// for one at all: a program named go is the go command, "go-toolchain go ..."
// is the go command, and "go-toolchain tool <name> ..." is a linked build
// tool. Anything else is the pipeline.
func LinkedGoArgs(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return nil, false
	}
	if name := strings.TrimSuffix(filepath.Base(argv[0]), ".exe"); name == "go" {
		return argv, true
	}
	if len(argv) >= 2 && argv[1] == "go" {
		return append([]string{"go"}, argv[2:]...), true
	}
	if len(argv) >= 3 && argv[1] == "tool" {
		return argv, true
	}
	return nil, false
}

// RunLinkedGo runs the go command or tool argv asks for and answers its
// exit status, or reports that argv asks for the pipeline instead.
func RunLinkedGo(argv []string) (int, bool) {
	goArgs, ok := LinkedGoArgs(argv)
	if !ok {
		return 0, false
	}
	return gocmd.Run(goArgs), true
}
