package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withAutoUpdateStubs installs test doubles for the auto-update seams and
// returns a pointer to a record of the re-exec call (nil if not invoked).
// It also forces a non-dev buildVersion and enables the gate. The caller's
// mockUpdater drives detect/isNewer/apply behavior.
type reexecRecord struct {
	called  bool
	exePath string
	args    []string
}

func withAutoUpdateStubs(t *testing.T, m selfUpdater) *reexecRecord {
	t.Helper()

	t.Setenv(autoUpdateEnvVar, "1")
	t.Setenv(autoUpdateDoneEnvVar, "")

	oldVersion := buildVersion
	buildVersion = "v0.0.1"
	t.Cleanup(func() { buildVersion = oldVersion })

	oldFactory := newUpdater
	newUpdater = func() selfUpdater { return m }
	t.Cleanup(func() { newUpdater = oldFactory })

	oldExe := currentExePath
	currentExePath = func() (string, error) { return "/fake/bin/go-toolchain", nil }
	t.Cleanup(func() { currentExePath = oldExe })

	rec := &reexecRecord{}
	oldReexec := reexecSelf
	reexecSelf = func(exePath string, args []string) error {
		rec.called = true
		rec.exePath = exePath
		rec.args = args
		return nil // pretend the child ran and exited; in tests we must return
	}
	t.Cleanup(func() { reexecSelf = oldReexec })

	return rec
}

func TestEnvTruthy(t *testing.T) {
	on := []string{"1", "true", "TRUE", "yes", "on", "anything", "  1  "}
	off := []string{"", "0", "false", "FALSE", "no", "off", "  ", "  off "}
	for _, v := range on {
		assert.True(t, envTruthy(v), "expected %q to be truthy", v)
	}
	for _, v := range off {
		assert.False(t, envTruthy(v), "expected %q to be falsey", v)
	}
}

func TestMaybeAutoUpdate_Disabled(t *testing.T) {
	t.Setenv(autoUpdateEnvVar, "")
	t.Setenv(autoUpdateDoneEnvVar, "")

	// newUpdater must never be called when the gate is off.
	oldFactory := newUpdater
	newUpdater = func() selfUpdater {
		t.Fatal("newUpdater called while auto-update disabled")
		return nil
	}
	t.Cleanup(func() { newUpdater = oldFactory })

	require.Nil(t, maybeAutoUpdate())
}

func TestMaybeAutoUpdate_AlreadyReexeced(t *testing.T) {
	t.Setenv(autoUpdateEnvVar, "1")
	t.Setenv(autoUpdateDoneEnvVar, "1") // we are the child

	oldFactory := newUpdater
	newUpdater = func() selfUpdater {
		t.Fatal("newUpdater called in a re-exec child")
		return nil
	}
	t.Cleanup(func() { newUpdater = oldFactory })

	require.Nil(t, maybeAutoUpdate())
}

func TestMaybeAutoUpdate_DevBuild(t *testing.T) {
	t.Setenv(autoUpdateEnvVar, "1")
	t.Setenv(autoUpdateDoneEnvVar, "")

	oldVersion := buildVersion
	buildVersion = "dev"
	t.Cleanup(func() { buildVersion = oldVersion })

	oldFactory := newUpdater
	newUpdater = func() selfUpdater {
		t.Fatal("newUpdater called for a dev build")
		return nil
	}
	t.Cleanup(func() { newUpdater = oldFactory })

	require.Nil(t, maybeAutoUpdate())
}

func TestMaybeAutoUpdate_AlreadyCurrent(t *testing.T) {
	m := &mockUpdater{version: "v0.0.1", found: true, newer: false}
	rec := withAutoUpdateStubs(t, m)

	require.Nil(t, maybeAutoUpdate())
	assert.False(t, rec.called, "should not re-exec when already current")
	assert.Equal(t, 0, m.applyCalls)
}

func TestMaybeAutoUpdate_NoReleaseFound(t *testing.T) {
	m := &mockUpdater{found: false, newer: true}
	rec := withAutoUpdateStubs(t, m)

	require.Nil(t, maybeAutoUpdate())
	assert.False(t, rec.called)
	assert.Equal(t, 0, m.applyCalls)
}

func TestMaybeAutoUpdate_DetectErrorFailsOpen(t *testing.T) {
	m := &mockUpdater{detectErr: assert.AnError}
	rec := withAutoUpdateStubs(t, m)

	// A flaky registry must not block the build.
	require.Nil(t, maybeAutoUpdate())
	assert.False(t, rec.called)
	assert.Equal(t, 0, m.applyCalls)
}

func TestMaybeAutoUpdate_ApplyErrorFailsOpen(t *testing.T) {
	m := &mockUpdater{version: "v0.0.200", found: true, newer: true, applyErr: assert.AnError}
	rec := withAutoUpdateStubs(t, m)

	require.Nil(t, maybeAutoUpdate())
	assert.False(t, rec.called, "must not re-exec when the update did not apply")
	assert.Equal(t, 1, m.applyCalls)
}

func TestMaybeAutoUpdate_NewerSucceedsAndReexecs(t *testing.T) {
	m := &mockUpdater{version: "v0.0.200", found: true, newer: true}
	rec := withAutoUpdateStubs(t, m)

	require.Nil(t, maybeAutoUpdate())
	assert.Equal(t, 1, m.applyCalls, "update should be applied")
	require.True(t, rec.called, "should re-exec after a successful update")
	assert.Equal(t, "/fake/bin/go-toolchain", rec.exePath)
	assert.Equal(t, os.Args[1:], rec.args, "re-exec must preserve the original arguments")
}

func TestReexecCommand(t *testing.T) {
	c := reexecCommand("/path/to/go-toolchain", []string{"matrix", "--json"})

	assert.Equal(t, []string{"/path/to/go-toolchain", "matrix", "--json"}, c.Args)
	assert.Equal(t, os.Stdout, c.Stdout)
	assert.Equal(t, os.Stderr, c.Stderr)
	assert.Equal(t, os.Stdin, c.Stdin)

	var found bool
	for _, e := range c.Env {
		if e == autoUpdateDoneEnvVar+"=1" {
			found = true
		}
	}
	assert.True(t, found, "child env must carry the done marker to prevent re-exec loops")
}
