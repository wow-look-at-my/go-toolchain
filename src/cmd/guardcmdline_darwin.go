package cmd

import (
	"bytes"
	"encoding/binary"
	"os"

	"golang.org/x/sys/unix"
)

// darwin has no /proc, so argv comes from the kernel's own copy.
// KERN_PROCARGS2 lays it out as: a 4-byte argc, the executable path, NUL
// padding, then argc NUL-terminated arguments.
func readCmdline(pid int) ([]string, bool) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(buf) < 4 {
		return nil, false
	}
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	if argc <= 0 {
		return nil, false
	}
	rest := buf[4:]

	// Step over the executable path and the NUL run padding it.
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil, false
	}
	rest = rest[end:]
	for len(rest) > 0 && rest[0] == 0 {
		rest = rest[1:]
	}

	argv := make([]string, 0, argc)
	for len(argv) < argc {
		i := bytes.IndexByte(rest, 0)
		if i < 0 {
			argv = append(argv, string(rest))
			break
		}
		argv = append(argv, string(rest[:i]))
		rest = rest[i+1:]
	}
	return argv, len(argv) > 0
}

func selfPID() int { return os.Getpid() }
