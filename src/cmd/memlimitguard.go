package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/memlimit"
)

// memLimitEnvVar gates injection of the cgroup→GOMEMLIMIT startup guard into
// every built main package. Injection is on by default; set it to a falsey
// value (0/false/no/off) to disable it for a build.
const memLimitEnvVar = "GO_TOOLCHAIN_AUTO_MEMLIMIT"

// envTruthy reports whether an environment variable value should be treated as
// "on". Any non-empty value other than an explicit falsey literal counts, so
// GO_TOOLCHAIN_AUTO_MEMLIMIT=0 (or false/no/off) disables it.
func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// injectMemLimitGuard writes the GOMEMLIMIT startup guard into every main
// package before the build compiles them, so each binary caps the Go heap at
// the container's cgroup memory limit instead of allocating until it is
// OOM-killed. The guard is a transient build artifact: cleanupMemLimitGuards
// deletes it once the build has consumed it, and checkDirtyInCI ignores it in
// every git state so a copy that outlives the build (or a stale one a repo
// committed under an older go-toolchain) never fails the dirty-tree check.
// Injection is idempotent.
func injectMemLimitGuard(quiet bool) error {
	if v, ok := os.LookupEnv(memLimitEnvVar); ok && !envTruthy(v) {
		return nil
	}
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

// cleanupMemLimitGuards removes the transient GOMEMLIMIT guards that
// injectMemLimitGuard wrote, once the build has compiled them in. This is what
// keeps the generated files from littering the working tree (and from failing
// the dirty-tree check). It is best-effort: a failed removal is reported but
// never fails the build. It honors the same kill switch as injection, so
// disabling the feature leaves the working tree untouched.
func cleanupMemLimitGuards() {
	if v, ok := os.LookupEnv(memLimitEnvVar); ok && !envTruthy(v) {
		return
	}
	if _, err := memlimit.CleanupAll(); err != nil {
		logger.Warn("  warning: failed to remove GOMEMLIMIT guard: %v", err)
	}
}
