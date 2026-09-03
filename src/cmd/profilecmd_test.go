package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/profile"
	gotest "github.com/wow-look-at-my/go-toolchain/src/test"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
)

// resetProfileState saves and restores the package-level profiling state a
// test mutates, so tests stay independent of ordering.
func resetProfileState(t *testing.T) {
	t.Helper()
	origCollector, origGraph := profileCollector, profileGraph
	origNoProfile, origJSON, origOut := noProfile, jsonOutput, outputDir
	origTrace, origHook := activeTrace, gotest.GraphArgFunc
	t.Cleanup(func() {
		profileCollector, profileGraph = origCollector, origGraph
		noProfile, jsonOutput, outputDir = origNoProfile, origJSON, origOut
		activeTrace, gotest.GraphArgFunc = origTrace, origHook
		profile.SetActive(nil)
	})
}

const testGraphJSON = `[
	{"ID":1,"Mode":"build","Package":"example.com/m/pkga","NeedBuild":true,
	 "ActionID":"aaaaaaaaaaaaaaaaaaaa",
	 "TimeStart":"2026-07-04T10:00:01Z","TimeDone":"2026-07-04T10:00:03Z"}
]`

// seedCollector installs an active collector whose single dump file already
// contains testGraphJSON, as if a go invocation had run and dumped it.
func seedCollector(t *testing.T) {
	t.Helper()
	profileCollector = profile.NewCollector(filepath.Join(t.TempDir(), "profile"))
	arg := profileCollector.GraphArg()
	path := arg[len("-debug-actiongraph="):]
	require.NoError(t, os.WriteFile(path, []byte(testGraphJSON), 0o644))
}

func TestInitBuildProfile_RespectsNoProfile(t *testing.T) {
	resetProfileState(t)

	noProfile = true
	profileCollector = nil
	gotest.GraphArgFunc = nil
	initBuildProfile()
	assert.Nil(t, profileCollector)
	assert.Nil(t, gotest.GraphArgFunc)
	assert.Equal(t, "", profile.GraphArg())

	noProfile = false
	initBuildProfile()
	assert.NotNil(t, profileCollector)
	require.NotNil(t, gotest.GraphArgFunc)
	assert.Contains(t, gotest.GraphArgFunc(), "-debug-actiongraph=")
}

func TestCaptureProfileTrace_RecordsLaneEvents(t *testing.T) {
	resetProfileState(t)
	seedCollector(t)
	profileGraph = nil
	activeTrace = gotrace.NewTrace()

	captureProfileTrace()

	require.Len(t, profileGraph, 1, "the parsed graph must be stashed for the final report")
	events := activeTrace.Events()
	require.Len(t, events, 1)
	assert.Equal(t, "pkga", events[0].Name)
	assert.Equal(t, "action", events[0].Category)
}

func TestEmitBuildProfile_WritesBothJSONFiles(t *testing.T) {
	t.Serial()
	resetProfileState(t)
	seedCollector(t)
	profileGraph = nil
	jsonOutput = true // suppress the console section in test output
	outputDir = filepath.Join(t.TempDir(), "build")
	tmpProfile := filepath.Join(profileDir(), "profile.json")
	os.Remove(tmpProfile)

	emitBuildProfile()

	for _, p := range []string{filepath.Join(outputDir, "profile.json"), tmpProfile} {
		data, err := os.ReadFile(p)
		require.NoError(t, err, p)
		assert.Contains(t, string(data), `"example.com/m/pkga"`)
		assert.Contains(t, string(data), `"total_actions": 1`)
	}
}

func TestEmitBuildProfile_SkipsCleanly(t *testing.T) {
	t.Serial()
	resetProfileState(t)

	// No collector at all (--no-profile or a non-building command).
	profileCollector = nil
	emitBuildProfile() // must not panic or write anything

	// Collector active but no graph dumped (vet-only path / failed build).
	profileCollector = profile.NewCollector(filepath.Join(t.TempDir(), "p"))
	profileGraph = nil
	outputDir = filepath.Join(t.TempDir(), "build")
	emitBuildProfile()
	_, err := os.Stat(filepath.Join(outputDir, "profile.json"))
	assert.True(t, os.IsNotExist(err), "no actiongraph: no profile.json")
}
