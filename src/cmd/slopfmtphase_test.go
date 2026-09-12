package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSlopfixLineReadsAFinding(t *testing.T) {
	t.Serial()
	hit, ok := parseSlopfixLine(`docs/CI.md:12:7: "three" is a number in a comment`)
	require.True(t, ok)
	assert.Equal(t, "docs/CI.md", hit.path)
	assert.Equal(t, 12, hit.line)
	assert.Equal(t, 7, hit.col)
	assert.Equal(t, "three", hit.number)
}

// A windows path carries a colon of its own, so the position is read from the
// right rather than from the leading colon in the line.
func TestParseSlopfixLineKeepsADriveLetterOnThePath(t *testing.T) {
	t.Serial()
	hit, ok := parseSlopfixLine(`C:\src\a.go:3:14: "3" is a number in a comment`)
	require.True(t, ok)
	assert.Equal(t, `C:\src\a.go`, hit.path)
	assert.Equal(t, 3, hit.line)
	assert.Equal(t, 14, hit.col)
}

func TestParseSlopfixLineRejectsWhatIsNotAFinding(t *testing.T) {
	t.Serial()
	for _, line := range []string{
		"",
		"a number in a comment is a count of what exists today",
		`a.go:x:y: "3" is a number in a comment`,
	} {
		_, ok := parseSlopfixLine(line)
		assert.False(t, ok, line)
	}
}

func TestBatchedCoversEveryFileOnce(t *testing.T) {
	t.Serial()
	in := []string{"a", "b", "c", "d", "e"}
	var seen []string
	for _, batch := range batched(in, 2) {
		assert.LessOrEqual(t, len(batch), 2)
		seen = append(seen, batch...)
	}
	assert.Equal(t, in, seen)
	assert.Nil(t, batched(nil, 2))
}

func TestSlopfixBinNameCarriesTheSuffixNTNeeds(t *testing.T) {
	t.Serial()
	assert.Equal(t, "slopfix.exe", slopfixBinName("windows"))
	assert.Equal(t, "slopfix", slopfixBinName("linux"))
}

func TestSlopfixDownloadURLPinsOneRelease(t *testing.T) {
	t.Serial()
	assert.Equal(t, "https://dl.pazer.build/slopfix?os=linux&arch=amd64",
		slopfixDownloadURL("", "linux", "amd64"))
	assert.Equal(t, "https://dl.pazer.build/slopfix?v=12&os=darwin&arch=arm64",
		slopfixDownloadURL("12", "darwin", "arm64"))
}

// A named binary that is not there names itself, rather than reporting a clean
// tree the scan never read.
func TestEnsureSlopfixFailsOnAMissingLocalBuild(t *testing.T) {
	t.Serial()
	t.Setenv(slopfixBinEnv, "/nonexistent/slopfix")
	_, err := ensureSlopfix()
	require.Error(t, err)
	assert.Contains(t, err.Error(), slopfixBinEnv)
}

// A tool that never starts must not pass for a clean tree. The stand-in
// carries no execute bit, which every kernel refuses alike. A malformed
// executable does not: darwin hands it to a shell, whose own refusal
// arrives as the exit code a finding uses.
func TestRunSlopfmtPhaseFailsWhenTheToolCannotStart(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	bin := filepath.Join(dir, "slopfix")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0644))

	t.Setenv(slopfixBinEnv, bin)
	err := runSlopfmtPhase(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not start")
}

// A finding is how the tool spends a failing exit, so the run still counts.
func TestSlopfixFindingsKeepsTheOutputOfANonZeroExit(t *testing.T) {
	t.Serial()
	if runtime.GOOS == "windows" {
		t.Skip("the fixture is a shell script, which CreateProcess refuses")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake.sh")
	script := "#!/bin/sh\necho 'a.go:3:14: \"3\" is a number in a comment'\nexit 1\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0755))

	hits, err := slopfixFindings(bin, []string{"a.go"})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "3", hits[0].number)
}

// The kernel killing the tool arrives as the same error type a finding does.
// Reading it as a finding reports a clean tree for a scan that read nothing,
// which is how a malformed binary passed on darwin.
func TestRunSlopfmtPhaseFailsWhenTheToolDiesBySignal(t *testing.T) {
	t.Serial()
	if runtime.GOOS == "windows" {
		t.Skip("no shell script and no POSIX signal here")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake.sh")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nkill -9 $$\n"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0644))

	t.Setenv(slopfixBinEnv, bin)
	err := runSlopfmtPhase(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "did not start")
}

func TestRunSlopfmtPhaseFailsWhenTheToolIsUnreachable(t *testing.T) {
	t.Serial()
	old := ensureSlopfixFunc
	ensureSlopfixFunc = func() (string, error) { return "", assert.AnError }
	defer func() { ensureSlopfixFunc = old }()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0644))
	assert.Error(t, runSlopfmtPhase(dir))
}
