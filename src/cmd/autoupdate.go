package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// autoUpdateEnvVar gates automatic, enforced self-update. When set to a truthy
// value, go-toolchain checks for a newer published release before doing any
// real work and, if one exists, updates itself in place and re-executes the
// original command on the freshly installed binary.
//
// The point is that a stale binary baked into an image — e.g. the Claude Code
// web image — heals itself on first use instead of silently running outdated
// build logic. (A stale build once mis-rewrote google.golang.org modules onto
// GitHub mirrors when a vanity-host probe timed out on a slow network; keeping
// the binary current is the durable guard against that class of regression.)
const autoUpdateEnvVar = "GO_TOOLCHAIN_AUTO_UPDATE"

// autoUpdateDoneEnvVar is set on the re-executed child so a single process tree
// never tries to auto-update twice. This is belt-and-suspenders on top of the
// version comparison, which on its own already prevents a loop once the new
// binary reports a version that is not older than the latest release.
const autoUpdateDoneEnvVar = "GO_TOOLCHAIN_AUTO_UPDATE_DONE"

// envTruthy reports whether an environment variable value should be treated as
// "on". Any non-empty value other than an explicit falsey literal counts, so
// GO_TOOLCHAIN_AUTO_UPDATE=0 (or false/no/off) disables it.
func envTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "0", "false", "no", "off":
		return false
	default:
		return true
	}
}

// currentExePath resolves the path of the running executable, following
// symlinks so the update lands on the real file rather than a symlink.
// Replaceable for testing.
var currentExePath = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

// reexecCommand builds the command that re-runs the updated binary with the
// original arguments, marking the child via autoUpdateDoneEnvVar and wiring
// through the parent's standard streams.
func reexecCommand(exePath string, args []string) *exec.Cmd {
	c := exec.Command(exePath, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	c.Env = append(os.Environ(), autoUpdateDoneEnvVar+"=1")
	return c
}

// reexecSelf replaces the current invocation with a fresh run of the
// (just-updated) binary at exePath. On success it does not return: it exits
// with the child's exit code. It only returns an error if the updated binary
// could not be launched at all. Replaceable for testing.
var reexecSelf = func(exePath string, args []string) error {
	if err := reexecCommand(exePath, args).Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		return fmt.Errorf("re-exec updated binary %s: %w", exePath, err)
	}
	os.Exit(0)
	return nil // unreachable
}

// maybeAutoUpdate performs an enforced self-update when autoUpdateEnvVar is set
// and a newer release exists, then re-execs onto it. It is a no-op when the
// gate is off, when this is already a re-exec child, or for dev builds that
// have no embedded version to compare against.
//
// Update *checks* fail open: a flaky registry must never block a build. The
// update being "enforced" means it is performed automatically (not merely
// suggested) whenever it is possible — not that work is blocked when the
// registry is unreachable. Running stale-but-working beats blocking every
// build on a transient network error.
func maybeAutoUpdate() error {
	if !envTruthy(os.Getenv(autoUpdateEnvVar)) || os.Getenv(autoUpdateDoneEnvVar) != "" {
		return nil
	}
	if buildVersion == "dev" {
		return nil
	}

	ctx := context.Background()
	u := newUpdater()

	latest, found, err := u.detect(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "⇒ Auto-update check failed (%v); continuing on %s\n", err, buildVersion)
		return nil
	}
	if !found || !u.isNewer(buildVersion) {
		return nil
	}

	exePath, err := currentExePath()
	if err != nil {
		fmt.Fprintf(os.Stderr, "⇒ Auto-update skipped (%v); continuing on %s\n", err, buildVersion)
		return nil
	}

	fmt.Printf("⇒ Auto-updating go-toolchain %s → %s (%s set)\n", buildVersion, latest, autoUpdateEnvVar)
	if err := u.applyUpdate(ctx, exePath); err != nil {
		fmt.Fprintf(os.Stderr, "⇒ Auto-update failed (%v); continuing on %s\n", err, buildVersion)
		return nil
	}
	fmt.Printf("⇒ Updated to %s; re-running\n", latest)

	return reexecSelf(exePath, os.Args[1:])
}
