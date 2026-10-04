package cmd

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// missingGenerator names the earliest `go run` source file of a directive that
// the directive's own directory does not hold, or "" when they are all there.
// A module zip carries the packages the module builds, and a generator is a
// separate main package that some modules leave out.
func missingGenerator(d generateDirective) string {
	args, err := splitGenerateCommand(d.Command)
	if err != nil || len(args) < 2 || args[0] != "go" || args[1] != "run" {
		return ""
	}
	dir := filepath.Dir(d.File)
	for _, a := range args[2:] {
		// The operands end at the program's own earliest flag, and a .go
		// name after that is an output the program writes, such as -stubs.
		if strings.HasPrefix(a, "-") {
			break
		}
		if !strings.HasSuffix(a, ".go") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, a)); os.IsNotExist(err) {
			return a
		}
	}
	return ""
}

// pendingDepDirectives keeps the dependency directives still owed their output.
// A directive whose generator the module never shipped is dropped loudly: it
// runs nowhere, so an approval for it only authorizes a command that fails.
func pendingDepDirectives(all []generateDirective) []generateDirective {
	var out []generateDirective
	for _, d := range all {
		if !owesOutput(d) {
			continue
		}
		if miss := missingGenerator(d); miss != "" {
			logger.Warn("⇒ Warning: %s:%d owes output, and %s is not in the module: %s", d.Label, d.Line, miss, d.Command)
			continue
		}
		out = append(out, d)
	}
	return out
}
