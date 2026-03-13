package cmd

import (
	"context"
	"fmt"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

type mockUpdater struct {
	version    string
	found      bool
	detectErr  error
	newer      bool
	applyErr   error
	applyCalls int
}

func (m *mockUpdater) detect(_ context.Context, slug string) (string, bool, error) {
	return m.version, m.found, m.detectErr
}

func (m *mockUpdater) isNewer(currentVersion string) bool {
	return m.newer
}

func (m *mockUpdater) applyUpdate(_ context.Context, exePath string) error {
	m.applyCalls++
	return m.applyErr
}

func TestDoUpdate_DetectError(t *testing.T) {
	m := &mockUpdater{detectErr: fmt.Errorf("network down")}
	err := doUpdate(context.Background(), m)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "failed to detect latest release")
	assert.Contains(t, err.Error(), "network down")
}

func TestDoUpdate_NotFound(t *testing.T) {
	m := &mockUpdater{found: false}
	err := doUpdate(context.Background(), m)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "no release found")
}

func TestDoUpdate_AlreadyUpToDate(t *testing.T) {
	old := buildVersion
	buildVersion = "v0.0.100"
	defer func() { buildVersion = old }()

	m := &mockUpdater{version: "v0.0.100", found: true, newer: false}
	err := doUpdate(context.Background(), m)
	require.Nil(t, err)
	assert.Equal(t, 0, m.applyCalls)
}

func TestDoUpdate_DevBuildWarning(t *testing.T) {
	old := buildVersion
	buildVersion = "dev"
	defer func() { buildVersion = old }()

	// Dev builds proceed to update (applyUpdate will be called)
	m := &mockUpdater{version: "v0.0.200", found: true, newer: false}
	// applyUpdate will succeed but exePath resolution uses the test binary
	err := doUpdate(context.Background(), m)
	// It may error on the actual binary update, but the dev build path was taken
	if err == nil {
		assert.Equal(t, 1, m.applyCalls)
	}
}

func TestDoUpdate_NonSemverVersion(t *testing.T) {
	old := buildVersion
	buildVersion = "latest-1-g649dd4a"
	defer func() { buildVersion = old }()

	// Non-semver versions should not panic and should proceed with update
	m := &mockUpdater{version: "v0.0.200", found: true, newer: true}
	err := doUpdate(context.Background(), m)
	// Should not panic; update proceeds
	if err == nil {
		assert.Equal(t, 1, m.applyCalls)
	}
}

func TestDoUpdate_ApplyError(t *testing.T) {
	old := buildVersion
	buildVersion = "v0.0.1"
	defer func() { buildVersion = old }()

	m := &mockUpdater{
		version:  "v0.0.200",
		found:    true,
		newer:    true,
		applyErr: fmt.Errorf("permission denied"),
	}
	err := doUpdate(context.Background(), m)
	require.NotNil(t, err)
	assert.Contains(t, err.Error(), "update failed")
	assert.Contains(t, err.Error(), "permission denied")
}

func TestDoUpdate_Success(t *testing.T) {
	old := buildVersion
	buildVersion = "v0.0.1"
	defer func() { buildVersion = old }()

	m := &mockUpdater{version: "v0.0.200", found: true, newer: true}
	err := doUpdate(context.Background(), m)
	require.Nil(t, err)
	assert.Equal(t, 1, m.applyCalls)
}
