package cmd

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	gocmd "cmd/go"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// LinkedGoArgs answers the go command line argv asks for, and whether it asks
// for one at all. A program named go is the go command.
func LinkedGoArgs(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return nil, false
	}
	if isGoName(argv[0]) {
		return argv, true
	}
	if len(argv) >= 3 && argv[1] == "tool" && linkedTools.Contains(argv[2]) {
		return argv, true
	}
	return nil, false
}

// linkedTools are the tools the fork's go command links, from
// _gosmopolitan/src/cmd/go/internal/selftool/tools.go.
var linkedTools = set.Of(
	"asm", "cgo", "compile", "covdata", "cover",
	"embedstd", "fix", "link", "preprofile", "vet",
)

// RunLinkedGo runs the go command or tool argv asks for and answers its
// exit status, or reports that argv asks for the pipeline instead.
func RunLinkedGo(argv []string) (int, bool) {
	goArgs, ok := LinkedGoArgs(argv)
	if !ok {
		return 0, false
	}
	if !isGoName(argv[0]) {
		return gocmd.Run(goArgs), true
	}
	self, err := goProgramPath(argv[0])
	if err != nil {
		logger.Error("go: %v", err)
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
}

// isGoName reports that path names a program called go, under either
// separator: this binary is a single program on every host, and its
// own filepath knows only the slash.
func isGoName(path string) bool {
	base := path[strings.LastIndexAny(path, `/\`)+1:]
	return strings.TrimSuffix(base, ".exe") == "go"
}
