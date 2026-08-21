package cosmocompat

import (
	"fmt"
	"os"
	"strings"
)

// xSysGap closes golang.org/x/sys/unix's cosmo build gap. Most files are
// arch-generic (their pristine source is already tagged for every linux
// GOARCH x/sys supports, or carries no arch suffix at all), so one cosmo
// copy covers every architecture; the syscall-number and type-layout
// tables genuinely differ per architecture and so are copied once per arch.
var xSysGap = gap{
	module:          "golang.org/x/sys",
	verifiedVersion: "v0.47.0",
	copies: []copySpec{
		{"unix/affinity_linux.go", "unix/affinity_cosmo.go", ""},
		{"unix/aliases.go", "unix/aliases_cosmo.go", ""},
		{"unix/auxv.go", "unix/auxv2_cosmo.go", ""},
		{"unix/bluetooth_linux.go", "unix/bluetooth_cosmo.go", ""},
		{"unix/constants.go", "unix/constants_cosmo.go", ""},
		{"unix/dirent.go", "unix/dirent_cosmo.go", ""},
		{"unix/env_unix.go", "unix/env_cosmo.go", ""},
		{"unix/fcntl.go", "unix/fcntl_cosmo.go", ""},
		{"unix/fdset.go", "unix/fdset_cosmo.go", ""},
		{"unix/ifreq_linux.go", "unix/ifreq_cosmo.go", ""},
		{"unix/ioctl_unsigned.go", "unix/ioctl_unsigned_cosmo.go", ""},
		{"unix/mremap.go", "unix/mremap_cosmo.go", ""},
		{"unix/pagesize_unix.go", "unix/pagesize_cosmo.go", ""},
		// race0.go/race.go's own tag pair (aix || (darwin && !race) || ...,
		// vs (darwin && race) || ...) is entirely OS-keyed and none of those
		// OSes is ever "cosmo", so a flat "cosmo" tag on both copies would
		// make them collide instead of staying mutually exclusive -- keep
		// the !race/race split explicitly instead.
		{"unix/race0.go", "unix/race0_cosmo.go", "!race"},
		{"unix/race.go", "unix/race_cosmo.go", "race"},
		// readv_unix.go holds the helpers Readv/Writev in syscall_linux.go call:
		// minIovec, appendBytes, readvRaceDetect, writevRaceDetect. Its own tag is
		// OS-keyed and never true under cosmo, so the cosmo build of
		// syscall2_cosmo.go has six undefined symbols without this copy.
		{"unix/readv_unix.go", "unix/readv_cosmo.go", ""},
		{"unix/readdirent_getdents.go", "unix/readdirent_cosmo.go", ""},
		{"unix/sockcmsg_unix.go", "unix/sockcmsg_cosmo.go", ""},
		{"unix/sockcmsg_unix_other.go", "unix/sockcmsgother_cosmo.go", ""},
		{"unix/syscall.go", "unix/syscall3_cosmo.go", ""},
		{"unix/syscall_unix.go", "unix/syscall_cosmo.go", ""},
		{"unix/sysvshm_linux.go", "unix/sysvshm2_cosmo.go", ""},
		{"unix/sysvshm_unix.go", "unix/sysvshm_cosmo.go", ""},
		{"unix/timestruct.go", "unix/timestruct_cosmo.go", ""},
		{"unix/vgetrandom_linux.go", "unix/vgetrandom_cosmo.go", ""},
		{"unix/zerrors_linux.go", "unix/zerrors_cosmo.go", ""},
		{"unix/zptrace_x86_linux.go", "unix/zptracex86_cosmo.go", ""},
		{"unix/zsyscall_linux.go", "unix/zsyscall_cosmo.go", ""},
		{"unix/ztypes_linux.go", "unix/ztypes_cosmo.go", ""},

		{"unix/zsysnum_linux_amd64.go", "unix/zsysnum_cosmo_amd64.go", ""},
		{"unix/ztypes_linux_amd64.go", "unix/ztypes_cosmo_amd64.go", ""},
		{"unix/zsyscall_linux_amd64.go", "unix/zsyscall_cosmo_amd64.go", ""},
		{"unix/zerrors_linux_amd64.go", "unix/zerrors_cosmo_amd64.go", ""},
		{"unix/syscall_linux_amd64.go", "unix/syscall4_cosmo_amd64.go", ""},

		{"unix/zsysnum_linux_arm64.go", "unix/zsysnum_cosmo_arm64.go", ""},
		{"unix/ztypes_linux_arm64.go", "unix/ztypes_cosmo_arm64.go", ""},
		{"unix/zsyscall_linux_arm64.go", "unix/zsyscall_cosmo_arm64.go", ""},
		{"unix/zerrors_linux_arm64.go", "unix/zerrors_cosmo_arm64.go", ""},
		{"unix/syscall_linux_arm64.go", "unix/syscall4_cosmo_arm64.go", ""},

		// syscall2_cosmo.go starts as this copy; patchPrlimit below then
		// rewrites its one incompatible function.
		{"unix/syscall_linux.go", "unix/syscall2_cosmo.go", ""},
	},
	// tagEdits: vgetrandom_unsupported.go's fallback ("not linux, or too
	// old a Go release for the real vgetrandom path") is also true under
	// cosmo, colliding with vgetrandom_cosmo.go's real implementation.
	tagEdits: []tagEdit{
		{"unix/vgetrandom_unsupported.go"},
	},
	overlays: map[string]string{
		"unix/syscall_cosmo_gc.go": "overlay/xsys_syscall_cosmo_gc.go.tmpl",
	},
	postPatch: patchXSysPrlimit,
}

// patchXSysPrlimit replaces syscall2_cosmo.go's copy of Prlimit, which
// upstream pulls from the standard "syscall" package with
// //go:linkname syscall.prlimit. The cosmo fork's syscall.prlimit carries
// no matching //go:linkname push pragma authorizing that pull, so the link
// fails; call the raw syscall directly instead.
//
// This drops the upstream side channel where a prlimit(RLIMIT_NOFILE) call
// also clears the "syscall" package's cached original limit, later read by
// os/exec.StartProcess to restore a child's file-descriptor limit. A
// consumer combining unix.Prlimit with os/exec under cosmo would see a
// stale limit propagate to the child; nothing else about this patch is
// observably different.
func patchXSysPrlimit(moduleDir string) error {
	path := moduleDir + "/unix/syscall2_cosmo.go"
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)

	const old = `//go:linkname syscall_prlimit syscall.prlimit
func syscall_prlimit(pid, resource int, newlimit, old *syscall.Rlimit) error

func Prlimit(pid, resource int, newlimit, old *Rlimit) error {
	// Just call the syscall version, because as of Go 1.21
	// it will affect starting a new process.
	return syscall_prlimit(pid, resource, (*syscall.Rlimit)(newlimit), (*syscall.Rlimit)(old))
}`

	const patched = `// The upstream x/sys pulls this from the "syscall" package with
// //go:linkname, so a prlimit(RLIMIT_NOFILE) here also clears the
// package's cached original limit and so is picked up by a later
// os/exec.StartProcess. The cosmo fork's syscall.prlimit carries no
// matching //go:linkname push pragma, so that pull cannot resolve here;
// call the raw syscall directly instead. A caller combining unix.Prlimit
// with os/exec under cosmo may see a stale fd limit propagate to a child.
func Prlimit(pid, resource int, newlimit, old *Rlimit) error {
	_, _, errno := Syscall6(SYS_PRLIMIT64, uintptr(pid), uintptr(resource), uintptr(unsafe.Pointer(newlimit)), uintptr(unsafe.Pointer(old)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}`

	if !strings.Contains(text, old) {
		return fmt.Errorf("cosmocompat: %s no longer contains the expected Prlimit block -- golang.org/x/sys must have changed it upstream; update patchXSysPrlimit in src/cosmocompat/tables_xsys.go", path)
	}
	text = strings.Replace(text, old, patched, 1)
	return os.WriteFile(path, []byte(text), 0o644)
}
