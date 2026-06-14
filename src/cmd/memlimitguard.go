package cmd

import (
	"fmt"
	"os"
	"strings"

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
// OOM-killed. Injection is idempotent; in CI a missing or stale guard surfaces
// as a dirty tree through checkDirtyInCI, which tells the developer to run
// go-toolchain locally and commit the generated files.
func injectMemLimitGuard(quiet bool) error {
	if v, ok := os.LookupEnv(memLimitEnvVar); ok && !envTruthy(v) {
		return nil
	}
	changed, err := memlimit.InjectAll()
	if err != nil {
		return fmt.Errorf("injecting GOMEMLIMIT guard: %w", err)
	}
	if len(changed) > 0 && !quiet {
		fmt.Printf("  GOMEMLIMIT guard written to %d package(s): %s\n",
			len(changed), strings.Join(changed, ", "))
	}
	return nil
}
