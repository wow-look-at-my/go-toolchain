package cmd

import (
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/slopfix"
)

// repairSweep is the prose repair, running beside phases that do not
// read it. Depth: docs/COMMENT-SCAN.md
//
// It takes every rule slopfix carries rather than the comment ones alone:
// comment length, the numbers said in words, the tombstones, the wrap and the
// STE wording. A rule that only warns leaves the author the work its own
// repair already knows how to do.
type repairSweep struct {
	done   chan struct{}
	result slopfix.TreeRepair
	took   time.Duration
}

// activeRepair is the sweep this run started, if it reached a module.
var activeRepair *repairSweep

// startRepair sweeps root on a goroutine and answers a handle. The
// caller must have found a go.mod: a repair is a write. The sweep is safe
// beside tidy and generate, but NOT beside vet.
func startRepair(root string) *repairSweep {
	scan := &repairSweep{done: make(chan struct{})}
	start := time.Now()
	go func() {
		defer close(scan.done)
		scan.result = slopfix.FixTree(root)
		scan.took = time.Since(start)
	}()
	return scan
}

// waitForRepair joins the sweep this run started and reports what it did.
// With no sweep running it returns straight away.
func waitForRepair() {
	scan := activeRepair
	activeRepair = nil
	if scan == nil {
		return
	}
	<-scan.done
	scan.report()
}

// report prints what the sweep did after the fact. The phase ran beside other
// output and cannot narrate itself while it works.
func (c *repairSweep) report() {
	result := c.result
	if len(result.Repaired) > 0 {
		logger.Output("⇒ prose repair: rewrote %d of %d files %s",
			len(result.Repaired), result.Read, fmtDuration(c.took))
	}
	// A cut sentence is gone from the tree, so this is the only record of it.
	for _, removal := range result.Removed {
		logger.Output("   %s: the repair cut %q", removal.Path, removal.Text)
	}
	// What is left carries no repair: a sentence with no clause seam to divide
	// at, or a count whose noun only the author can name.
	for _, finding := range result.Findings {
		logger.WarnFile(finding.Path, "%s:%d: [%s] %s: %s",
			finding.Path, finding.Line, finding.ID, finding.Rule, finding.Fix)
	}
	for _, kept := range result.Kept {
		logger.WarnFile(kept.Path, "%s:%d: [%s] %s", kept.Path, kept.LineNo, kept.ID, kept.Tell)
	}
	if tl := GetTimeline(); tl != nil {
		end := time.Now()
		tl.Record("prose repair", "prose-repair", end.Add(-c.took), end, false)
	}
}
