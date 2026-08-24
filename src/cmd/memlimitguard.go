package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/memlimit"
)

// injectMemLimitGuard writes the GOMEMLIMIT startup guard into every main package before the build, capping the
// Go heap at the container's cgroup limit instead of an OOM kill. The guard is transient: cleanupMemLimitGuards
// deletes it after the build, and checkDirtyInCI ignores it. ensureGuardExcluded (called first) hides it from git
// so VCS stamping never sees it. No disable flag: the opt-out is the runtime GOMEMLIMIT=off.
func injectMemLimitGuard(quiet bool) error {
	// Exclude BEFORE writing, so no git status during the build window (Go's version stamping) can see it.
	ensureGuardExcluded()
	changed, err := memlimit.InjectAll()
	if err != nil {
		return fmt.Errorf("injecting GOMEMLIMIT guard: %w", err)
	}
	if len(changed) > 0 && !quiet {
		logger.Info("  GOMEMLIMIT guard written to %d package(s): %s",
			len(changed), strings.Join(changed, ", "))
	}
	return nil
}

// ensureGuardExcluded lists the transient guard in the repo's clone-local git exclude file
// (.git/info/exclude, via `git rev-parse --git-path` for linked worktrees), so `git status`
// never sees it. Without this, Go 1.24+'s version stamping runs `git status --porcelain`
// while the guard exists, sees an untracked file, and stamps every binary "+dirty" on a
// clean checkout. The exclude file must be used instead of .gitignore: it lives under .git/,
// outside the working tree, so writing it cannot itself dirty the tree. The entry is left in
// place (clone-local; also hides a stale guard from an interrupted build). Best-effort: any
// failure just means the old +dirty stamp, never a broken build.
func ensureGuardExcluded() {
	out, err := exec.Command("git", "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return // no git, or not inside a repository: nothing to exclude
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == memlimit.GuardFileName {
			return // already excluded
		}
	}
	entry := memlimit.GuardFileName + "\n"
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		entry = "\n" + entry
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(entry)
}

// cleanupMemLimitGuards removes the guards injectMemLimitGuard wrote. Best-effort: a failed removal never fails the build.
func cleanupMemLimitGuards() {
	if _, err := memlimit.CleanupAll(); err != nil {
		logger.Warn("  warning: failed to remove GOMEMLIMIT guard: %v", err)
	}
}
