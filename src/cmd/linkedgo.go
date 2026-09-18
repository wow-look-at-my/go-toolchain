package cmd

import (
	"os"
	"strings"

	gocmd "cmd/go"
)

// LinkedGoArgs answers the go command line argv asks for, and whether it asks
// for a single at all: a program named go is the go command, "go-toolchain go
// ..." is the go command, and "go-toolchain tool <name> ..." is a linked
// build tool. Anything else is the pipeline.
func LinkedGoArgs(argv []string) ([]string, bool) {
	if len(argv) == 0 || os.Getenv(linkedGoEnv) == "" {
		return nil, false
	}
	if isGoName(argv[0]) {
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

// linkedGoEnv marks a process the pipeline started.
const linkedGoEnv = "GO_TOOLCHAIN_LINKED_GO"

// RunLinkedGo runs the go command or tool argv asks for and answers its
// exit status, or reports that argv asks for the pipeline instead.
func RunLinkedGo(argv []string) (int, bool) {
	goArgs, ok := LinkedGoArgs(argv)
	if !ok {
		return 0, false
	}
	exe, err := os.Executable()
	if err != nil {
		return gocmd.Run(goArgs), true
	}
	return gocmd.RunAs(goArgs, selfGoCommand(exe)), true
}

// selfGoCommand is how the go command starts itself again: exe alone when that
// name is already the go link, and exe under its go subcommand otherwise.
func selfGoCommand(exe string) []string {
	if isGoName(exe) {
		return []string{exe}
	}
	return []string{exe, "go"}
}

// isGoName reports that path names a program called go, under either separator:
// this binary runs on every host, and its own filepath knows only the slash.
func isGoName(path string) bool {
	base := path[strings.LastIndexAny(path, `/\`)+1:]
	return strings.TrimSuffix(base, ".exe") == "go"
}
