// Command ratchetjudge runs from the default branch's checkout, as its
// .github/ratchet names it. It runs that checkout's ratchet tests against the
// branch checked out at its one argument, and fails unless each passes there.
// A branch that drops the ratchet, or weakens what it checks, fails the
// default branch's tests.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// testFile holds the ratchet's tests, in both checkouts.
const testFile = "src/cmd/ratchet_test.go"

var testName = regexp.MustCompile(`(?m)^func (Test\w+)\(`)

func main() {
	if err := judge(".", os.Args[1:], goTest); err != nil {
		logger.Error("%v", err)
		os.Exit(1)
	}
}

func judge(base string, args []string, test func(tree string, names []string) (string, error)) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: ratchetjudge BRANCH_CHECKOUT")
	}
	head := args[0]
	source, err := os.ReadFile(filepath.Join(base, testFile))
	if err != nil {
		return err
	}
	var names []string
	for _, m := range testName.FindAllStringSubmatch(string(source), -1) {
		names = append(names, m[1])
	}
	if len(names) == 0 {
		return fmt.Errorf("ratchetjudge: %s holds no test", testFile)
	}

	tree, err := os.MkdirTemp("", "ratchet-head")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tree)
	if out, err := exec.Command("git", "-C", head, "worktree", "add", "--quiet", "--detach", tree, "HEAD").CombinedOutput(); err != nil {
		return fmt.Errorf("ratchetjudge: checking out %s: %w: %s", head, err, out)
	}
	defer exec.Command("git", "-C", head, "worktree", "remove", "--force", tree).Run()
	if err := os.WriteFile(filepath.Join(tree, testFile), source, 0o644); err != nil {
		return err
	}

	out, err := test(tree, names)
	var failed []string
	for _, name := range names {
		if !strings.Contains(out, "--- PASS: "+name+" ") {
			failed = append(failed, name)
		}
	}
	if err != nil || len(failed) > 0 {
		return fmt.Errorf("ratchetjudge: the default branch's ratchet tests fail on this branch. %s did not pass:\n%s",
			strings.Join(failed, ", "), out)
	}
	return nil
}

// goTest runs the named tests of the command package in tree.
func goTest(tree string, names []string) (string, error) {
	cmd := exec.Command("go", "test", "-count=1", "-v", "-run", "^("+strings.Join(names, "|")+")$", "./src/cmd")
	cmd.Dir = tree
	out, err := cmd.CombinedOutput()
	return string(out), err
}
