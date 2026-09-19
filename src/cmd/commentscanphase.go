package cmd

import (
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/slopfix/commentfix"
)

// commentScan is the comment repair, running beside phases that do not
// read it. Depth: docs/COMMENT-SCAN.md
type commentScan struct {
	done   chan struct{}
	result commentfix.TreeResult
	took   time.Duration
}

<<<<<<< HEAD
// repairCommentNumbers rewrites a single file's comments in place and reports
// whether it changed. A cut sentence is named, because the repair threw prose
// away and the author is the only a single who can put the meaning back.
func repairCommentNumbers(path, src string) bool {
	fixed := commentfix.Fix(path, src)
	if !fixed.Changed {
		// Nothing swapped. A finding here is a single the repair does not cover.
		for _, hit := range commentNumberFindings(path, src) {
			logger.WarnFile(path, "%s:%d:%d: %q is a number in a comment: %s",
				path, hit.Line, hit.Col, hit.Number, commentfix.Remedy)
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
func commentNumberFindings(path, src string) []commentfix.Hit {
	seen := set.New[int]()
	var out []commentfix.Hit
	for _, hit := range commentfix.Check(path, src) {
		if seen.Contains(hit.Line) {
			continue
		}
		seen.Add(hit.Line)
		out = append(out, hit)
=======
// activeCommentScan is the sweep this run started, if it reached a module.
var activeCommentScan *commentScan

// startCommentScan sweeps root on a goroutine and answers a handle. The
// caller must have found a go.mod: a repair is a write. The sweep is safe
// beside tidy and generate, but NOT beside vet.
func startCommentScan(root string) *commentScan {
	scan := &commentScan{done: make(chan struct{})}
	start := time.Now()
	go func() {
		defer close(scan.done)
		scan.result = commentfix.FixTree(root)
		scan.took = time.Since(start)
	}()
	return scan
}

// waitForCommentScan joins the sweep this run started and reports what it did.
// With no sweep running it returns straight away.
func waitForCommentScan() {
	scan := activeCommentScan
	activeCommentScan = nil
	if scan == nil {
		return
>>>>>>> origin/claude/module-path-comment
	}
	<-scan.done
	scan.report()
}

<<<<<<< HEAD
// commentScanFiles returns every file under root the rule reads.
func commentScanFiles(root string) []string {
	// Where the root is not a module, the modules below it are the whole tree.
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	rootIsModule := err == nil
	var out []string
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if commentScanSkipDir(root, path, d.Name(), rootIsModule) {
				return filepath.SkipDir
			}
			return nil
		}
		if !commentfix.Supported(path) {
			return nil
		}
		if info, err := d.Info(); err == nil && info.Size() > commentScanMaxFileBytes {
			return nil
		}
		out = append(out, path)
		return nil
	})
	return out
}

// commentScanSkipDir reports whether the walk stops at this directory.
func commentScanSkipDir(root, path, name string, rootIsModule bool) bool {
	if path == root {
		return false
	}
	if strings.HasPrefix(name, ".") || name == outputDir || commentScanSkipDirs.Contains(name) {
		return true
	}
	if gomod.IsGitSubmodule(path) {
		return true
	}
	return rootIsModule && gomod.IsNestedModule(path)
=======
// report prints what the sweep did after the fact. The phase ran beside other
// output and cannot narrate itself while it works.
func (c *commentScan) report() {
	result := c.result
	if len(result.Repaired) > 0 {
		logger.Output("⇒ comment scan: repaired %d of %d files %s",
			len(result.Repaired), result.Read, fmtDuration(c.took))
	}
	// A cut sentence is gone from the tree, so this is the only record of it.
	for _, removal := range result.Removed {
		logger.Output("   %s: the comment repair cut %q", removal.Path, removal.Text)
	}
	// slopfix repairs every finding it reports, so this loop is a guard: a
	// warning here says the rule and its repair have come apart.
	for _, finding := range result.Findings {
		logger.WarnFile(finding.Path, "%s:%d:%d: %q is a number in a comment: %s",
			finding.Path, finding.Line, finding.Col, finding.Number, commentfix.Remedy)
	}
	if tl := GetTimeline(); tl != nil {
		end := time.Now()
		tl.Record("comment scan", "comment-scan", end.Add(-c.took), end, false)
	}
>>>>>>> origin/claude/module-path-comment
}
