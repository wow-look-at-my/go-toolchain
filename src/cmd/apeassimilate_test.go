package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// apePrologue is the shape a real fat APE carries: a $m branch per machine,
// each ending in the printf that writes the header over the first bytes. The
// two headers differ so a test can prove the host's branch is the one read.
const apePrologue = apeMagic + `
                                   '
: <<'__APE__'
__APE__
m=$(uname -m 2>/dev/null) || m=x86_64
if [ "$m" = x86_64 ] || [ "$m" = amd64 ]; then
  o="$(command -v "$0")"
  exec 7<> "$o" || exit 121
  printf '\177ELF\002\001\001\011\000\000\000\000\000\000\000\000\002\000>\000' >&7
  exec 7<&-
  exec "$0" "$@"
fi
if [ "$m" = aarch64 ] || [ "$m" = arm64 ]; then
  o="$(command -v "$0")"
  printf '\177ELF\002\001\001\011\000\000\000\000\000\000\000\000\002\000\267\000' >&7
  exec "$0" "$@"
fi
echo 'APE: unsupported platform' >&2
exit 1
`

// hostMachineByte is the one byte the two branches disagree on: the e_machine
// field, EM_X86_64 (62) against EM_AARCH64 (183).
func hostMachineByte(t *testing.T) byte {
	t.Helper()
	switch runtime.GOARCH {
	case "amd64":
		return 62
	case "arm64":
		return 183
	}
	t.Skipf("no APE prologue branch for %s", runtime.GOARCH)
	return 0
}

func writeAPE(t *testing.T, prologue string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artifact")
	// Pad past the prologue so the file is longer than the header, like a real
	// APE whose images follow the shell part.
	body := prologue + strings.Repeat("\x00", 4096)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o755))
	return path
}

func TestAssimilateAPEWritesTheHostBranchHeader(t *testing.T) {
	want := hostMachineByte(t)
	path := writeAPE(t, apePrologue)

	require.NoError(t, assimilateAPE(path))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, []byte("\x7fELF"), got[:4], "the kernel execs the file only with this magic")
	assert.Equal(t, want, got[18], "e_machine comes from this host's branch")
}

// The header is written over the front, never inserted: an APE's offsets are
// baked in, so a byte of drift breaks every one of them.
func TestAssimilateAPEKeepsTheFileLength(t *testing.T) {
	hostMachineByte(t)
	path := writeAPE(t, apePrologue)
	before, err := os.Stat(path)
	require.NoError(t, err)

	require.NoError(t, assimilateAPE(path))

	after, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, before.Size(), after.Size())
}

func TestAssimilateAPELeavesANativeBinaryAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "native")
	native := append([]byte("\x7fELF\x02\x01\x01"), make([]byte, 128)...)
	require.NoError(t, os.WriteFile(path, native, 0o755))

	require.NoError(t, assimilateAPE(path))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, native, got)
}

// A prologue this code cannot read is an error, not a silent pass: the caller
// warns, and a suite that execs the handoff path would otherwise fail with a
// bare exit 121 and no reason.
func TestAssimilateAPERejectsAPrologueItCannotRead(t *testing.T) {
	hostMachineByte(t)
	path := writeAPE(t, apeMagic+"\nm=$(uname -m)\necho no branches here\n")

	err := assimilateAPE(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no self-assimilating branch")
}

func TestAPEELFHeaderRejectsAPayloadThatIsNotAnELFHeader(t *testing.T) {
	hostMachineByte(t)
	prologue := strings.Replace(apePrologue, `\177ELF`, `\177NOT`, -1)

	_, err := apeELFHeader(prologue)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an ELF header")
}

func TestShellPrintfBytes(t *testing.T) {
	tests := []struct {
		desc   string
		format string
		want   []byte
	}{
		{"octal escapes", `\177\000\002`, []byte{0x7f, 0, 2}},
		{"short octal escapes", `\0\7\77`, []byte{0, 7, 63}},
		{"literal bytes", `ELF>`, []byte("ELF>")},
		{"named escapes", `\t\n\\`, []byte{9, 10, '\\'}},
		{"a doubled percent is one percent", `%%`, []byte("%")},
		{"an octal escape stops at three digits", `\1111`, []byte{'I', '1'}},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			got, err := shellPrintfBytes(tt.format)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestShellPrintfBytesRejectsWhatItCannotDecode(t *testing.T) {
	tests := []struct {
		desc   string
		format string
		want   string
	}{
		{"a conversion specification", `%s`, "conversion specification"},
		{"an unknown escape", `\q`, "unknown escape"},
		{"a trailing backslash", `ELF\`, "ends in a backslash"},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			_, err := shellPrintfBytes(tt.format)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}
