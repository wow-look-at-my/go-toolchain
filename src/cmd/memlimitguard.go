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

// injectMemLimitGuard writes the GOMEMLIMIT startup guard into every main
// package before the build compiles them, so each binary caps the Go heap at
// the container's cgroup memory limit instead of allocating until it is
// OOM-killed. The guard is a transient build artifact: cleanupMemLimitGuards
// deletes it once the build has consumed it, and checkDirtyInCI ignores it in
// every git state so a copy that outlives the build (or a stale one a repo
// committed under an older go-toolchain) never fails the dirty-tree check.
// ensureGuardExcluded (called first) additionally hides the guard from git
// itself, so the go command's VCS stamping never sees it either.
// Injection is idempotent and unconditional: there is deliberately no flag or
// environment variable to disable it. A knob here would eventually be set and
// left set, shipping binaries that allocate until the kernel OOM-kills them,
// and the guard already defers to an explicit GOMEMLIMIT at run time
// (GOMEMLIMIT=off is the per-deployment kill switch), which is the correct
// layer for that decision.
func injectMemLimitGuard(quiet bool) error {
	// Exclude BEFORE writing the guard, so no git status taken during the
	// build window can ever see it — Go's version stamping in particular.
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

// ensureGuardExcluded lists the transient guard in the repository's
// clone-local git exclude file (.git/info/exclude, resolved through
// `git rev-parse --git-path` so linked worktrees get the right file), making
// the injected file invisible to `git status` for the whole build window.
//
// Without it, Go's main-module version stamping (Go 1.24+) — which runs
// `git status --porcelain` while the guard exists — sees an untracked file,
// sets vcs.modified, and stamps every built binary's Main.Version "+dirty":
// false provenance for a clean checkout (checkDirtyInCI's guard exclusion is
// go-toolchain's own logic, invisible to the go command).
//
// The exclude file — NOT .gitignore — is load-bearing: it lives under .git/,
// outside the working tree, so writing it cannot itself dirty the tree,
// which is exactly why the guard must never be appended to .gitignore (see
// ensureBuildDirInGitignore's migration cleanup). The entry is appended
// idempotently and left in place: it is clone-local, and keeping it also
// stops a stale guard left by an interrupted build from polluting later
// stamps. Best-effort like the .gitignore upkeep: any failure just means
// the old +dirty stamp, never a broken build.
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

// cleanupMemLimitGuards removes the transient GOMEMLIMIT guards that
// injectMemLimitGuard wrote, once the build has compiled them in. This is what
// keeps the generated files from littering the working tree (and from failing
// the dirty-tree check). It is best-effort: a failed removal is reported but
// never fails the build. Like injection it is unconditional, so it also sheds a
// stale guard a repo committed under an older go-toolchain.
func cleanupMemLimitGuards() {
	if _, err := memlimit.CleanupAll(); err != nil {
		logger.Warn("  warning: failed to remove GOMEMLIMIT guard: %v", err)
	}
}
