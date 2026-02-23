package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestRunProfileUnknownType(t *testing.T) {
	mock := runner.NewMock()

	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
	}()

	profileType = "invalid"
	profileOutput = "test.pprof"
	profileNoPprof = true

	err := runProfileWithRunner(mock, []string{})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "unknown profile type")
}

func TestRunProfileCPU(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "cpu.pprof")

	mock := runner.NewMock()
	// Mock will return success, we create a fake profile file
	// Mock returns success by default

	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	oldTime := benchTime
	oldCount := benchCount
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
		benchTime = oldTime
		benchCount = oldCount
	}()

	profileType = "cpu"
	profileOutput = outFile
	profileNoPprof = true
	benchTime = "1s"
	benchCount = 1

	// Create the profile file so the os.Stat check passes
	os.WriteFile(outFile, []byte("fake profile"), 0644)

	err := runProfileWithRunner(mock, []string{})
	assert.Nil(t, err)

	// Verify correct args were passed
	calls := mock.Calls()
	assert.GreaterOrEqual(t, len(calls), 1)
	call := calls[0]
	assert.True(t, call.IsCmd("go", "test"))
	assert.True(t, call.HasArg("-cpuprofile"))
	assert.True(t, call.HasArg("-bench"))
}

func TestRunProfileMem(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "mem.pprof")

	mock := runner.NewMock()
	// Mock returns success by default

	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	oldTime := benchTime
	oldCount := benchCount
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
		benchTime = oldTime
		benchCount = oldCount
	}()

	profileType = "mem"
	profileOutput = outFile
	profileNoPprof = true
	benchTime = ""
	benchCount = 1

	os.WriteFile(outFile, []byte("fake profile"), 0644)

	err := runProfileWithRunner(mock, []string{})
	assert.Nil(t, err)

	calls := mock.Calls()
	assert.GreaterOrEqual(t, len(calls), 1)
	assert.True(t, calls[0].HasArg("-memprofile"))
}

func TestRunProfileMutex(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "mutex.pprof")

	mock := runner.NewMock()
	// Mock returns success by default

	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	oldTime := benchTime
	oldCount := benchCount
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
		benchTime = oldTime
		benchCount = oldCount
	}()

	profileType = "mutex"
	profileOutput = outFile
	profileNoPprof = true
	benchTime = ""
	benchCount = 1

	os.WriteFile(outFile, []byte("fake profile"), 0644)

	err := runProfileWithRunner(mock, []string{})
	assert.Nil(t, err)

	calls := mock.Calls()
	assert.GreaterOrEqual(t, len(calls), 1)
	assert.True(t, calls[0].HasArg("-mutexprofile"))
}

func TestRunProfileBlock(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "block.pprof")

	mock := runner.NewMock()
	// Mock returns success by default

	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	oldTime := benchTime
	oldCount := benchCount
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
		benchTime = oldTime
		benchCount = oldCount
	}()

	profileType = "block"
	profileOutput = outFile
	profileNoPprof = true
	benchTime = ""
	benchCount = 1

	os.WriteFile(outFile, []byte("fake profile"), 0644)

	err := runProfileWithRunner(mock, []string{})
	assert.Nil(t, err)

	calls := mock.Calls()
	assert.GreaterOrEqual(t, len(calls), 1)
	assert.True(t, calls[0].HasArg("-blockprofile"))
}

func TestRunProfileWithPackageArg(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "cpu.pprof")

	mock := runner.NewMock()
	// Mock returns success by default

	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	oldTime := benchTime
	oldCount := benchCount
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
		benchTime = oldTime
		benchCount = oldCount
	}()

	profileType = "cpu"
	profileOutput = outFile
	profileNoPprof = true
	benchTime = ""
	benchCount = 1

	os.WriteFile(outFile, []byte("fake profile"), 0644)

	err := runProfileWithRunner(mock, []string{"./pkg/..."})
	assert.Nil(t, err)

	calls := mock.Calls()
	assert.GreaterOrEqual(t, len(calls), 1)
	assert.True(t, calls[0].HasArg("./pkg/..."))
}

func TestRunProfileWithCount(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "cpu.pprof")

	mock := runner.NewMock()
	// Mock returns success by default

	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	oldTime := benchTime
	oldCount := benchCount
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
		benchTime = oldTime
		benchCount = oldCount
	}()

	profileType = "cpu"
	profileOutput = outFile
	profileNoPprof = true
	benchTime = ""
	benchCount = 3

	os.WriteFile(outFile, []byte("fake profile"), 0644)

	err := runProfileWithRunner(mock, []string{})
	assert.Nil(t, err)

	calls := mock.Calls()
	assert.GreaterOrEqual(t, len(calls), 1)
	assert.True(t, calls[0].HasArg("-count"))
}

func TestRunProfileGoTestFails(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "cpu.pprof")

	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcess(nil, assert.AnError), nil
	}

	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	oldTime := benchTime
	oldCount := benchCount
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
		benchTime = oldTime
		benchCount = oldCount
	}()

	profileType = "cpu"
	profileOutput = outFile
	profileNoPprof = true
	benchTime = ""
	benchCount = 1

	err := runProfileWithRunner(mock, []string{})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "profiling failed")
}

func TestRunProfileNoOutputFile(t *testing.T) {
	tmpDir := t.TempDir()
	outFile := filepath.Join(tmpDir, "nonexistent", "cpu.pprof")

	mock := runner.NewMock()
	// Mock returns success by default

	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	oldTime := benchTime
	oldCount := benchCount
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
		benchTime = oldTime
		benchCount = oldCount
	}()

	profileType = "cpu"
	profileOutput = outFile
	profileNoPprof = true
	benchTime = ""
	benchCount = 1

	// Don't create the file - simulates no benchmarks found
	err := runProfileWithRunner(mock, []string{})
	assert.NotNil(t, err)
	assert.Contains(t, err.Error(), "profile output not created")
}

func TestRunProfile(t *testing.T) {
	// Exercise the entry point (will fail but covers the code path)
	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
	}()

	profileType = "invalid"
	profileOutput = "test.pprof"
	profileNoPprof = true

	err := runProfile(profileCmd, []string{})
	assert.NotNil(t, err)
}

func TestRunProfileDefaultOutput(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(oldWd)

	mock := runner.NewMock()
	// Mock returns success by default

	oldType := profileType
	oldOutput := profileOutput
	oldNoPprof := profileNoPprof
	oldTime := benchTime
	oldCount := benchCount
	defer func() {
		profileType = oldType
		profileOutput = oldOutput
		profileNoPprof = oldNoPprof
		benchTime = oldTime
		benchCount = oldCount
	}()

	profileType = "cpu"
	profileOutput = "" // Use default
	profileNoPprof = true
	benchTime = ""
	benchCount = 1

	// Create the default output file
	os.WriteFile(filepath.Join(tmpDir, "profile_cpu.pprof"), []byte("fake"), 0644)

	err := runProfileWithRunner(mock, []string{})
	assert.Nil(t, err)
}
