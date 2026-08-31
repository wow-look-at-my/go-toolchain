package cmd

import (
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/go-toolchain/src/cache"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/profile"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
)

// noProfile disables the per-action build profile (--no-profile).
var noProfile bool

var (
	// profileCollector accumulates actiongraph dump paths; nil when disabled.
	profileCollector *profile.Collector
	// profileGraph is the parsed+merged actiongraph, stashed by captureProfileTrace and reused by emitBuildProfile.
	profileGraph []profile.Action
)

// profileDir holds the profile artifacts, next to trace.json. go opens the
// dumps itself, so the base is the host's.
func profileDir() string {
	return filepath.Join(argListTempDir(hostos.GOOS()), "go-toolchain-profile")
}

// initBuildProfile activates actiongraph collection for this run (unless
// --no-profile). Called at the start of the root and matrix commands; the
// injection sites (runBuild, test.RunTests) pick the collector up through
// profile.GraphArg without any plumbing.
func initBuildProfile() {
	if noProfile {
		return
	}
	profileCollector = profile.NewCollector(profileDir())
	profile.SetActive(profileCollector)
	// src/test can't import src/profile (import cycle), so hand it the hook directly.
	gotest.GraphArgFunc = profile.GraphArg
}

// captureProfileTrace parses the collected actiongraph dumps and records
// per-action lane events into the Chrome trace. It must run BEFORE run()'s
// deferred trace write; the parsed rows are stashed for emitBuildProfile.
// The cache outcomes read here are best-effort (the stats socket may still
// have events in flight); the final report re-snapshots after it drains.
func captureProfileTrace() {
	if profileCollector == nil {
		return
	}
	profileGraph = profile.LoadGraphs(profileCollector.Files(), os.Stderr)
	if activeTrace == nil || len(profileGraph) == 0 {
		return
	}
	var outcomes map[string]cache.ActionOutcome
	if statsListener != nil {
		outcomes = statsListener.Actions()
	}
	profile.AddTraceEvents(activeTrace, profileGraph, outcomes)
}

// emitBuildProfile joins the actiongraph with the per-action cache outcomes
// and emits the profile: the console section, build/profile.json +
// $TMPDIR/go-toolchain-profile/profile.json, and the CI step-summary table.
//
// Called from printCacheStats(close=true) — after the cache daemon has
// drained (the web counters are final) and the stats listener has closed
// (every per-action event has been delivered). Skips cleanly when no
// actiongraph was collected (vet-only paths, --no-profile, failed builds
// that never reached go build/test).
func emitBuildProfile() {
	if profileCollector == nil {
		return
	}
	if profileGraph == nil {
		profileGraph = profile.LoadGraphs(profileCollector.Files(), os.Stderr)
	}
	if len(profileGraph) == 0 {
		return
	}
	var (
		outcomes map[string]cache.ActionOutcome
		totals   *profile.CacheTotals
		overflow uint64
	)
	if statsListener != nil {
		outcomes = statsListener.Actions()
		overflow = statsListener.ActionsOverflow()
		totals = cacheTotalsFromStats(statsListener.Stats())
	}
	r := profile.BuildReport(profileGraph, outcomes, totals, runWebSummary(), overflow)
	if !jsonOutput {
		r.PrintConsole(os.Stdout)
	}
	if err := r.WriteJSON(filepath.Join(outputDir, "profile.json"), filepath.Join(profileDir(), "profile.json")); err != nil {
		logger.Warn("⇒ Warning: build profile: write profile.json: %v", err)
	}
	if err := r.AppendStepSummary(); err != nil {
		logger.Warn("⇒ Warning: build profile: step summary: %v", err)
	}
}

// runWebSummary is the whole run's web tier, not one component's. A
// namespaced cacheprog runs standalone and never touches the daemon, so a
// pipeline whose every phase is namespaced leaves the daemon holding an index
// it loaded and nothing else -- a live remote then reads as a dead one. Both
// sources are folded together, and nothing is double counted because a daemon
// connection's Server reports no web summary at all.
func runWebSummary() *cache.WebSummary {
	var merged cache.WebSummary
	first := true
	for _, ws := range []*cache.WebSummary{daemonWebSummary(), listenerWebSummary()} {
		if ws == nil {
			continue
		}
		cache.MergeWebSummary(&merged, *ws, first)
		first = false
	}
	if first {
		return nil
	}
	return &merged
}

// daemonWebSummary is the daemon's own tier, final after its Close.
func daemonWebSummary() *cache.WebSummary {
	if cacheDaemon == nil {
		return nil
	}
	return cacheDaemon.WebSummary()
}

// listenerWebSummary is what the standalone cacheprogs reported over the stats socket.
func listenerWebSummary() *cache.WebSummary {
	if statsListener == nil {
		return nil
	}
	return statsListener.WebSummary()
}

// cacheTotalsFromStats converts the listener aggregate into the profile's
// cache totals block.
func cacheTotalsFromStats(ss *cache.ServerStats) *profile.CacheTotals {
	ct := &profile.CacheTotals{
		LocalHits: ss.Local.Hits.Load(),
		LocalPuts: ss.Local.Puts.Load(),
		Misses:    ss.Misses.Load(),
	}
	if ss.Remote != nil {
		ct.RemoteHits = ss.Remote.Hits.Load()
		ct.RemotePuts = ss.Remote.Puts.Load()
	}
	if ss.Batch != nil {
		ct.Prefetched = ss.Batch.Populated.Load()
		ct.PrefetchUsed = ss.Batch.Used.Load()
	}
	return ct
}
