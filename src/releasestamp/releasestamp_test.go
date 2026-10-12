package releasestamp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bufferWithSlot surrounds one empty slot with other bytes, the way it sits in a binary.
func bufferWithSlot() []byte {
	buf := append([]byte(strings.Repeat("\xaa", 32)), releaseStampMagic...)
	buf = append(buf, make([]byte, 1+releaseStampPayload)...)
	return append(buf, []byte(strings.Repeat("\xbb", 32))...)
}

func TestStampedSlotReadsBackTheNumber(t *testing.T) {
	buf := bufferWithSlot()
	ok, err := stampRelease(buf, "412")
	require.NoError(t, err)
	assert.True(t, ok)
	got, err := readReleaseStamp(buf)
	require.NoError(t, err)
	assert.Equal(t, "412", got)
}

func TestStampingTouchesNothingOutsideTheSlot(t *testing.T) {
	buf := bufferWithSlot()
	size := len(buf)
	_, err := stampRelease(buf, "9")
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("\xaa", 32), string(buf[:32]))
	assert.Equal(t, strings.Repeat("\xbb", 32), string(buf[size-32:]))
	assert.Len(t, buf, size)
}

func TestRestampingLeavesNoTailOfTheLongerNumber(t *testing.T) {
	buf := bufferWithSlot()
	_, err := stampRelease(buf, "123456")
	require.NoError(t, err)
	_, err = stampRelease(buf, "7")
	require.NoError(t, err)
	got, err := readReleaseStamp(buf)
	require.NoError(t, err)
	assert.Equal(t, "7", got)
	assert.NotContains(t, string(buf), "23456")
}

func TestABinaryWithNoSlotIsLeftAlone(t *testing.T) {
	buf := make([]byte, 512)
	ok, err := stampRelease(buf, "1")
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, make([]byte, 512), buf)
}

func TestTwoSlotsAreRefused(t *testing.T) {
	buf := append(bufferWithSlot(), bufferWithSlot()...)
	_, err := stampRelease(buf, "1")
	assert.ErrorContains(t, err, "2 release stamp slots")
}

func TestAnEmptyOrOversizeNumberIsRefused(t *testing.T) {
	_, err := stampRelease(bufferWithSlot(), "")
	assert.ErrorIs(t, err, errStampVersionEmpty)
	_, err = stampRelease(bufferWithSlot(), strings.Repeat("9", releaseStampPayload+1))
	assert.ErrorContains(t, err, "the slot holds")
}

func TestATruncatedSlotIsRefused(t *testing.T) {
	buf := append(append([]byte{}, releaseStampMagic...), 0)
	_, err := stampRelease(buf, "1")
	assert.ErrorIs(t, err, errStampSlotTruncated)
}

func TestTheMagicIsSixteenBytes(t *testing.T) {
	assert.Equal(t, "\x00GOTOOLCHAIN-REL", string(releaseStampMagic))
}

func TestStampDirStampsSlotsAndRewritesChecksums(t *testing.T) {
	dir := t.TempDir()
	slotted := filepath.Join(dir, "tool_cosmo_fat")
	plain := filepath.Join(dir, "other_linux_amd64")
	require.NoError(t, os.WriteFile(slotted, bufferWithSlot(), 0o755))
	require.NoError(t, os.WriteFile(plain, []byte("no slot here"), 0o755))
	before, err := fileSHA256(slotted)
	require.NoError(t, err)
	plainSum, err := fileSHA256(plain)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "checksums.txt"),
		[]byte(plainSum+"  other_linux_amd64\n"+before+"  tool_cosmo_fat\n"), 0o644))

	stamped, err := StampDir(dir, "88")
	require.NoError(t, err)
	assert.Equal(t, []string{"tool_cosmo_fat"}, stamped)

	data, err := os.ReadFile(slotted)
	require.NoError(t, err)
	got, err := readReleaseStamp(data)
	require.NoError(t, err)
	assert.Equal(t, "88", got)

	info, err := os.Stat(slotted)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), info.Mode().Perm())

	after, err := fileSHA256(slotted)
	require.NoError(t, err)
	assert.NotEqual(t, before, after)
	sums, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	require.NoError(t, err)
	assert.Equal(t, plainSum+"  other_linux_amd64\n"+after+"  tool_cosmo_fat\n", string(sums))
}

func TestStampDirWithoutChecksumsStillStamps(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tool_cosmo_fat"), bufferWithSlot(), 0o755))
	stamped, err := StampDir(dir, "3")
	require.NoError(t, err)
	assert.Equal(t, []string{"tool_cosmo_fat"}, stamped)
	assert.NoFileExists(t, filepath.Join(dir, "checksums.txt"))
}

func TestStampDirFailsOnABinaryWithTwoSlots(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tool_cosmo_fat"), append(bufferWithSlot(), bufferWithSlot()...), 0o755))
	_, err := StampDir(dir, "1")
	assert.ErrorContains(t, err, "tool_cosmo_fat")
}
