package cmd

import (
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/slopfix"
)

// commentScan is the comment repair, running beside phases that do not
// read it. Depth: docs/COMMENT-SCAN.md
type commentScan struct {
	done   chan struct{}
	result slopfix.TreeRepair
	took   time.Duration
}

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
		scan.result = slopfix.FixTree(root)
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
	}
	<-scan.done
	scan.report()
}

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
	// An ste finding has no repair by design, so these are what the sweep
	// leaves for the author rather than a sign the rule and its repair parted.
	for _, finding := range result.Findings {
		logger.WarnFile(finding.Path, "%s:%d: %s: %s", finding.Path, finding.Line, finding.Rule, finding.Detail)
	}
	// A tombstone no whole-line deletion resolves is the other such case.
	for _, kept := range result.Kept {
		logger.WarnFile(kept.Path, "%s:%d: %s: %s", kept.Path, kept.LineNo, kept.ID, kept.Tell)
	}
	if tl := GetTimeline(); tl != nil {
		end := time.Now()
		tl.Record("comment scan", "comment-scan", end.Add(-c.took), end, false)
	}
}
