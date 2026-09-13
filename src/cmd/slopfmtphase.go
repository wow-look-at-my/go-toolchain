package cmd

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-containers/set"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/slopfix/commentnumbers"
	"github.com/wow-look-at-my/slopfix/treecomments"
)

// slopfmtSkipDirs hold text nobody here authored.
var slopfmtSkipDirs = set.Of("vendor", "node_modules", "testdata")

// slopfmtMaxFileBytes is where a file stops being prose and becomes a blob.
const slopfmtMaxFileBytes = 1 << 20

// runSlopfmtPhase REPAIRS every number stated in a comment, anywhere in the
// tree. Nothing resolves an import or starts a compiler, so it must stay ahead
// of every other phase: that is what it buys. A finding the repair could not
// swap is reported, because the repair cut that sentence rather than guess at
// it. Depth: docs/COMMENT-SCAN.md
func runSlopfmtPhase(root string) {
	st := logStep("comment scan")
	// The rule reads a parse table a generate step writes, and a binary compiled
	// before that step ran carries none: this run generated them for the NEXT
	// build of this binary. Saying so beats reporting a clean tree nobody read.
	if missing := treecomments.Missing(); len(missing) > 0 {
		logger.Warn("⇒ Warning: no comment was read for %s: this binary was built "+
			"before their parse tables existed, and the build that follows has them",
			strings.Join(missing, ", "))
		st.done()
		return
	}
	repaired := 0
	for _, path := range slopfmtFiles(root) {
		src, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if repairCommentNumbers(path, string(src)) {
			repaired++
		}
	}
	if repaired > 0 {
		st.noteOutput()
	}
	st.done()
}

// repairCommentNumbers rewrites a single file's comments in place and reports
// whether it changed. A cut sentence is named, because the repair threw prose
// away and the author is the only a single who can put the meaning back.
func repairCommentNumbers(path, src string) bool {
	fixed := commentnumbers.Fix(path, src)
	if !fixed.Changed {
		// Nothing swapped. A finding here is a single the repair does not cover.
		for _, hit := range commentNumberFindings(path, src) {
			logger.WarnFile(path, "%s:%d:%d: %q is a number in a comment: %s",
				path, hit.Line, hit.Col, hit.Number, commentnumbers.Remedy)
		}
		return false
	}
	info, err := os.Stat(path)
	mode := os.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path, []byte(fixed.Text), mode); err != nil {
		logger.Warn("⇒ Warning: could not write the comment repair for %s: %v", path, err)
		return false
	}
	for _, cut := range fixed.Removed {
		logger.WarnFile(path, "%s: the comment repair cut a sentence no rewrite covers: %q", path, cut)
	}
	return true
}

// commentNumberFindings keeps a finding per line rather than per number,
// because the repair is a rewrite of the line whatever it counts.
func commentNumberFindings(path, src string) []commentnumbers.Hit {
	seen := set.New[int]()
	var out []commentnumbers.Hit
	for _, hit := range commentnumbers.Check(path, src) {
		if seen.Contains(hit.Line) {
			continue
		}
		seen.Add(hit.Line)
		out = append(out, hit)
	}
	return out
}

// slopfmtFiles returns every file under root the rule reads.
func slopfmtFiles(root string) []string {
	// Where the root is not a module, the modules below it are the whole tree.
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	rootIsModule := err == nil
	var out []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if slopfmtSkipDir(root, path, d.Name(), rootIsModule) {
				return filepath.SkipDir
			}
			return nil
		}
		if !commentnumbers.Supported(path) {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() > slopfmtMaxFileBytes {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out
}

// slopfmtSkipDir reports whether the walk stops at this directory.
func slopfmtSkipDir(root, path, name string, rootIsModule bool) bool {
	if path == root {
		return false
	}
	if strings.HasPrefix(name, ".") || name == outputDir || slopfmtSkipDirs.Contains(name) {
		return true
	}
	if gomod.IsGitSubmodule(path) {
		return true
	}
	return rootIsModule && gomod.IsNestedModule(path)
}
