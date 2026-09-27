package cmd

import (
<<<<<<< HEAD
	"os"
=======
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
>>>>>>> origin/master
	"strings"

	gocmd "cmd/go"
)

// LinkedGoArgs answers the go command line argv asks for, and whether it asks
<<<<<<< HEAD
// for a single at all: a program named go is the go command, "go-toolchain go
// ..." is the go command, and "go-toolchain tool <name> ..." is a linked
// build tool. Anything else is the pipeline.
func LinkedGoArgs(argv []string) ([]string, bool) {
	if len(argv) == 0 || !PipelineStartedGo() {
=======
// for one at all. A program named go is the go command.
func LinkedGoArgs(argv []string) ([]string, bool) {
	if len(argv) == 0 {
>>>>>>> origin/master
		return nil, false
	}
	if isGoName(argv[0]) {
		return argv, true
	}
<<<<<<< HEAD
	if len(argv) >= 2 && argv[1] == "go" {
		return append([]string{"go"}, argv[2:]...), true
	}
	if len(argv) >= 3 && argv[1] == "tool" {
=======
	if len(argv) >= 3 && argv[1] == "tool" && linkedTools[argv[2]] {
>>>>>>> origin/master
		return argv, true
	}
	return nil, false
}

<<<<<<< HEAD
// PipelineStartedGo reports that the pipeline started this process.
func PipelineStartedGo() bool { return os.Getenv(linkedGoEnv) != "" }

// linkedGoEnv marks a process the pipeline started.
const linkedGoEnv = "GO_TOOLCHAIN_LINKED_GO"
=======
// linkedTools are the tools the fork's go command links, from
// _gosmopolitan/src/cmd/go/internal/selftool/tools.go.
var linkedTools = map[string]bool{
	"asm": true, "cgo": true, "compile": true, "covdata": true, "cover": true,
	"embedstd": true, "fix": true, "link": true, "preprofile": true, "vet": true,
}
>>>>>>> origin/master

// RunLinkedGo runs the go command or tool argv asks for and answers its
// exit status, or reports that argv asks for the pipeline instead.
func RunLinkedGo(argv []string) (int, bool) {
	goArgs, ok := LinkedGoArgs(argv)
	if !ok {
		return 0, false
	}
<<<<<<< HEAD
	exe, err := os.Executable()
	if err != nil {
		return gocmd.Run(goArgs), true
	}
	return gocmd.RunAs(goArgs, selfGoCommand(exe)), true
}

// selfGoCommand is the command line the go command starts itself again
// under: exe alone when exe is the go link, since that name is the go
// command, and exe under its go subcommand otherwise.
func selfGoCommand(exe string) []string {
	if isGoName(exe) {
		return []string{exe}
	}
	return []string{exe, "go"}
=======
	if !isGoName(argv[0]) {
		return gocmd.Run(goArgs), true
	}
	self, err := goProgramPath(argv[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "go: %v\n", err)
		return 2, true
	}
	return gocmd.RunAs(goArgs, []string{self}), true
}

// goProgramPath answers the path of the program named go that argv0 started.
// The go command starts itself again by this path. The path of this
// executable would start the pipeline instead.
func goProgramPath(argv0 string) (string, error) {
	if strings.ContainsAny(argv0, `/\`) {
		return filepath.Abs(argv0)
	}
	path, err := exec.LookPath(argv0)
	if err != nil {
		return "", fmt.Errorf("finding the program %s on PATH: %w", argv0, err)
	}
	return filepath.Abs(path)
>>>>>>> origin/master
}

// isGoName reports that path names a program called go, under either
// separator: this binary is a single program on every host, and its
// own filepath knows only the slash.
func isGoName(path string) bool {
	base := path[strings.LastIndexAny(path, `/\`)+1:]
	return strings.TrimSuffix(base, ".exe") == "go"
}
