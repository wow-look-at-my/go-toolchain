package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// cacheRecoveryEnv, when set to "1", marks the process as the cache-recovery
// retry: the remote build cache is disabled and a separate, never-remote-
// populated local cache directory is used, so the build recomputes everything
// from source. It guards against infinite recursion (recovery runs at most once)
// and is read by parseBuildCacheConfig (disable remote) and buildCacheDir
// (fresh local dir).
const cacheRecoveryEnv = "GO_TOOLCHAIN_CACHE_RECOVERY"

// inCacheRecovery reports whether this process is the cache-recovery retry.
func inCacheRecovery() bool { return os.Getenv(cacheRecoveryEnv) == "1" }

// looksLikeCachePoison reports whether a build failure message carries an
// unmistakable signature of the shared build cache having served a mis-keyed
// object that the in-line cacheprog guards (outputID hash, build-id action,
// module-index refusal) did not catch on this run. These are never produced by
// an ordinary source or compile error:
//
//   - "is not in std" / "corrupt index" — a poisoned or mis-keyed Go module
//     index served for a standard-library directory.
//   - export-data cross-contamination — a package whose cached export data
//     declares a different package name. It surfaces as BOTH an
//     `"<path>" imported as <other>` line and "undefined: <pkg>" references.
//     Both are required so a legitimate unused-aliased-import vet finding
//     (`"x" imported as y and not used`, which has no "undefined:") is not
//     mistaken for poison.
func looksLikeCachePoison(msg string) bool {
	if strings.Contains(msg, "is not in std") || strings.Contains(msg, "corrupt index") {
		return true
	}
	if strings.Contains(msg, "imported as ") && strings.Contains(msg, "undefined: ") {
		return true
	}
	return false
}

// ShouldRetryForCachePoison reports whether a failed build should be retried
// with the cache bypassed because its error looks like the shared build cache
// served a poisoned object. It is false during the retry itself, so recovery
// happens at most once.
func ShouldRetryForCachePoison(err error) bool {
	return err != nil && !inCacheRecovery() && looksLikeCachePoison(err.Error())
}

// recoveryCommand builds the child go-toolchain invocation for a cache-recovery
// retry: the same executable and arguments as this process, with the recovery
// env var set so the child disables the remote cache and uses a fresh local
// cache. Split out from RetryWithoutCache so the construction is unit-testable
// without spawning a process.
func recoveryCommand() (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	c := exec.Command(exe, os.Args[1:]...)
	c.Env = append(os.Environ(), cacheRecoveryEnv+"=1")
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c, nil
}

// RetryWithoutCache re-runs go-toolchain once with the remote cache disabled and
// a fresh local cache, so a poisoned shared cache can never hard-fail a build:
// everything is recomputed from source. It returns the exit code to propagate.
func RetryWithoutCache() int {
	fmt.Fprintf(os.Stderr, "\n⇒ Build failed with the signature of a poisoned build cache. Retrying once with the remote cache disabled and a fresh local cache (recomputing from source)...\n")
	c, err := recoveryCommand()
	if err != nil {
		fmt.Fprintf(os.Stderr, "⇒ cache recovery: cannot resolve executable: %v\n", err)
		return 1
	}
	err = c.Run()
	if err == nil {
		fmt.Fprintf(os.Stderr, "\n⇒ Recovery build succeeded with the cache bypassed. The shared remote cache appears to hold a poisoned object — investigate and evict it (the cache server's module-index refusal and cache-version purge are the durable fix).\n")
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		fmt.Fprintf(os.Stderr, "\n⇒ Recovery build also failed; the failure is likely not cache-related.\n")
		return ee.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "⇒ cache recovery: retry failed to start: %v\n", err)
	return 1
}

// buildCacheDir is the local build-cache directory. During cache recovery it is
// a separate directory so the retry starts from an empty cache that is never
// populated from the remote tier — the normal directory may still hold the
// poison (materialized during the failed first run) that triggered recovery.
func buildCacheDir() string {
	name := "buildcache"
	if inCacheRecovery() {
		name = "buildcache-recovery"
	}
	return filepath.Join(cacheHome(), name)
}
