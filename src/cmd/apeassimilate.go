package cmd

import (
	"bytes"
	"fmt"
	"os"
	"runtime"
	"strings"
)

// apeMagic starts every Cosmopolitan APE polyglot. The kernel cannot exec a
// file that starts with it, so the shell reads the file as a script.
const apeMagic = "MZqFpD='"

// apePrologueLimit bounds how much of a file is read to find the prologue. The
// shell part sits at the front, ahead of the embedded images.
const apePrologueLimit = 64 << 10

// assimilateAPE rewrites an APE in place into the native ELF the kernel execs
// directly. It does what the file's own prologue does on its first run:
//
//	exec 7<> "$o" || exit 121
//	printf '\177ELF\002...' >&7
//
// The header comes out of the prologue of the file being assimilated, so this
// writes what the loader would have written, not a header of our own.
//
// A caller assimilates a copy it owns, before anything execs it. The point is
// that the copy can then live on a read-only path: the loader's `exec 7<>`
// needs write access and exits 121 without it, which is what a dats sandbox
// gives a suite.
//
// A file that does not start with apeMagic is already native and is left
// alone. On a host whose prologue has no self-assimilating branch this returns
// an error: linux/arm64 refuses the fat APE outright, and darwin/arm64 execs it
// through a separate loader that never writes to the file, so neither needs
// this and neither is a case a caller can fix.
func assimilateAPE(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	head := make([]byte, apePrologueLimit)
	n, err := f.ReadAt(head, 0)
	if n == 0 {
		return fmt.Errorf("reading %s: %w", path, err)
	}
	head = head[:n]

	if !bytes.HasPrefix(head, []byte(apeMagic)) {
		return nil
	}

	elfHeader, err := apeELFHeader(string(head))
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if len(elfHeader) > n {
		return fmt.Errorf("%s: ELF header is longer than the file", path)
	}
	_, err = f.WriteAt(elfHeader, 0)
	return err
}

// apeArchGuard is the prologue's test for the host machine. The prologue reads
// `uname -m` into $m and branches on it.
func apeArchGuard() (string, error) {
	switch runtime.GOARCH {
	case "amd64":
		return `if [ "$m" = x86_64 ]`, nil
	case "arm64":
		return `if [ "$m" = aarch64 ]`, nil
	}
	return "", fmt.Errorf("no APE prologue branch is known for %s", runtime.GOARCH)
}

// apeELFHeader pulls the host's ELF header out of an APE prologue: the payload
// of the one `printf ... >&7` inside the branch for this machine. Fd 7 is the
// file itself, opened at offset 0, so that payload IS the header, byte for
// byte.
func apeELFHeader(prologue string) ([]byte, error) {
	guard, err := apeArchGuard()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(prologue, "\n")
	inBranch := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, guard):
			inBranch = true
			continue
		case trimmed == "fi":
			inBranch = false
			continue
		}
		if !inBranch || !strings.HasPrefix(trimmed, "printf '") || !strings.HasSuffix(trimmed, "' >&7") {
			continue
		}
		payload := strings.TrimSuffix(strings.TrimPrefix(trimmed, "printf '"), "' >&7")
		header, err := shellPrintfBytes(payload)
		if err != nil {
			return nil, err
		}
		if !bytes.HasPrefix(header, []byte("\x7fELF")) {
			return nil, fmt.Errorf("the prologue's printf payload is not an ELF header")
		}
		return header, nil
	}
	return nil, fmt.Errorf("found no self-assimilating branch for this machine in the APE prologue")
}

// shellPrintfBytes decodes a POSIX printf format string that carries binary
// data: octal escapes, the named control escapes, and literal bytes. It
// rejects anything else rather than guessing, because a wrong byte here
// corrupts the binary it is written into.
func shellPrintfBytes(format string) ([]byte, error) {
	named := map[byte]byte{'a': 7, 'b': 8, 'f': 12, 'n': 10, 'r': 13, 't': 9, 'v': 11, '\\': '\\', '\'': '\''}
	out := make([]byte, 0, len(format))
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c == '%' {
			if i+1 < len(format) && format[i+1] == '%' {
				out = append(out, '%')
				i++
				continue
			}
			return nil, fmt.Errorf("the printf payload has a conversion specification")
		}
		if c != '\\' {
			out = append(out, c)
			continue
		}
		if i+1 >= len(format) {
			return nil, fmt.Errorf("the printf payload ends in a backslash")
		}
		i++
		if b, ok := named[format[i]]; ok {
			out = append(out, b)
			continue
		}
		if format[i] < '0' || format[i] > '7' {
			return nil, fmt.Errorf("unknown escape \\%c in the printf payload", format[i])
		}
		value := 0
		digits := 0
		for digits < 3 && i < len(format) && format[i] >= '0' && format[i] <= '7' {
			value = value*8 + int(format[i]-'0')
			digits++
			i++
		}
		i--
		if value > 0xff {
			return nil, fmt.Errorf("octal escape \\%o in the printf payload is not a byte", value)
		}
		out = append(out, byte(value))
	}
	return out, nil
}
