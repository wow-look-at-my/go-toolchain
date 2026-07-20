package cmd

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/mod/modfile"
)

// WaitWithProgress waits for the check to complete, showing progress if it takes too long.
// Returns the list of outdated dependencies.
func (dc *DepChecker) WaitWithProgress() []OutdatedDep {
	if dc == nil {
		return nil
	}

	// Set up Ctrl+C handler to skip
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	go func() {
		select {
		case <-sigCh:
			dc.Cancel()
			cancel()
		case <-ctx.Done():
		}
	}()

	// Wait with progress display (throttled to 1/sec, no reprints if unchanged)
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	showProgress := false
	lastPct := -1
	startWait := time.Now()

	for {
		select {
		case <-dc.doneCh:
			elapsed := time.Since(startWait)
			if showProgress {
				logger.Info(" %sdone.%s %s", colorGreen, colorReset, fmtDuration(elapsed))
			}
			dc.mu.Lock()
			result := dc.results
			listTime := dc.listDepsTime
			liveChecks := dc.liveChecks
			checked := dc.checked
			total := dc.total
			dc.mu.Unlock()
			if showProgress && elapsed > 5*time.Second {
				logger.Info("    deps: list=%s, checked=%d/%d (%d live)",
					fmtDuration(listTime), checked, total, liveChecks)
			}
			return result
		case <-ticker.C:
			// Show progress if waiting more than 500ms
			if time.Since(startWait) > 500*time.Millisecond {
				checked, total := dc.Progress()
				pct := 0
				if total > 0 {
					pct = checked * 100 / total
				}
				// Only print if progress changed
				if pct != lastPct {
					if !showProgress {
						showProgress = true
						logger.Info("Checking for dependency updates... %d%%", pct)
					} else {
						logger.Info(" %d%%", pct)
					}
					lastPct = pct
				}
			}
		case <-ctx.Done():
			if showProgress {
				logger.Info(" skipped")
			}
			return nil
		}
	}
}

// PrintOutdatedDeps prints warnings for outdated dependencies
func PrintOutdatedDeps(deps []OutdatedDep) {
	if len(deps) == 0 {
		return
	}

	logger.Info("")
	logger.Warn("Outdated git dependencies:")
	for _, dep := range deps {
		current := shortenVersion(dep.Version)
		update := shortenVersion(dep.Update)
		logger.Info("    %s: %s -> %s", dep.Path, current, update)
	}
	logger.Info("    Run 'go get -u' to update")
}

// shortenVersion shortens a pseudo-version for display
func shortenVersion(v string) string {
	parts := strings.Split(v, "-")
	if len(parts) >= 3 {
		hash := parts[len(parts)-1]
		if len(hash) >= 7 {
			return hash[:7]
		}
		return hash
	}
	return v
}

// WaitForOutdatedDeps waits for the dependency check to complete and prints results.
// Dependencies from the same org/prefix as the current module are automatically updated.
// Returns true if any dependencies were auto-updated (caller should rebuild).
func WaitForOutdatedDeps(dc *DepChecker) bool {
	if dc == nil {
		return false
	}
	deps := dc.WaitWithProgress()

	// Record to the pipeline timeline
	if pipelineTimeline != nil {
		pipelineTimeline.Record("Dependency check", "deps", dc.start, time.Now(), dc.err != nil)
	}

	// Get auto-update prefix from current module
	autoUpdatePrefix := getAutoUpdatePrefix()

	// Separate auto-update deps from manual deps
	var toAutoUpdate, manual []OutdatedDep
	for _, dep := range deps {
		if autoUpdatePrefix != "" && strings.HasPrefix(dep.Path, autoUpdatePrefix) {
			toAutoUpdate = append(toAutoUpdate, dep)
		} else {
			manual = append(manual, dep)
		}
	}

	// Auto-update trusted dependencies
	if len(toAutoUpdate) > 0 {
		autoUpdateDeps(toAutoUpdate)
	}

	// Print remaining manual deps
	PrintOutdatedDeps(manual)

	return len(toAutoUpdate) > 0
}

// getAutoUpdatePrefix returns the org prefix from the current module path.
// e.g., "github.com/org/repo" -> "github.com/org/"
// e.g., "gitlab.com/group/repo" -> "gitlab.com/group/"
func getAutoUpdatePrefix() string {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		return ""
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil || f.Module == nil {
		return ""
	}

	// Extract host + org: "host.com/org/repo" -> "host.com/org/"
	parts := strings.Split(f.Module.Mod.Path, "/")
	if len(parts) >= 2 {
		return parts[0] + "/" + parts[1] + "/"
	}
	return ""
}

// autoUpdateDeps runs go get -u for each dependency
func autoUpdateDeps(deps []OutdatedDep) {
	logger.Info("")
	s := logStep("Auto-updating trusted dependencies")
	s.noteOutput()
	for _, dep := range deps {
		current := shortenVersion(dep.Version)
		update := shortenVersion(dep.Update)
		logger.Info("    %s: %s -> %s", dep.Path, current, update)

		cmd := exec.Command("go", "get", "-u", dep.Path+"@latest")
		if err := cmd.Run(); err != nil {
			logger.Warn("    WARNING: %s failed to update: %v", dep.Path, err)
		}
	}
	// Run go mod tidy to clean up
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Run()
	s.done()
}
