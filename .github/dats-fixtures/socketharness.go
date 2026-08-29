// socketharness reproduces a coding agent's own tool-execution plumbing: it
// spawns the binary its arguments name, with stdout wired through a
// UNIX-domain socketpair (not a bare pipe) instead of a pipe/file/terminal,
// exactly what a Node/Bun child_process does for a tool call's stdio. It
// exports OPENCODE_PID naming itself as the reader (as opencode really does
// for its bash tool's children) and prints the child's exit code and
// whether the guard's own refusal text appeared on stderr, so a dats test
// can assert on plain stdout without depending on process-tree internals.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func main() {
	args := os.Args[1:]
	wrongReader := false
	if len(args) > 0 && args[0] == "--wrong-reader" {
		wrongReader = true
		args = args[1:]
	}
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: socketharness [--wrong-reader] <binary> [args...]")
		os.Exit(2)
	}

	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, "socketpair:", err)
		os.Exit(2)
	}
	readerEnd := os.NewFile(uintptr(fds[0]), "socket-reader")
	childStdout := os.NewFile(uintptr(fds[1]), "socket-writer")

	// --wrong-reader names a pid that is neither this harness (the real
	// reader) nor any real ancestor of the child, so the allowance must not
	// fire -- the negative control proving the fix is not "always allow
	// sockets", only "allow when the reader really is the recognized agent".
	readerPID := os.Getpid()
	if wrongReader {
		readerPID = 1
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = childStdout
	var errBuf strings.Builder
	cmd.Stderr = &errBuf
	cmd.Env = append(os.Environ(), "OPENCODE=1", "OPENCODE_PID="+strconv.Itoa(readerPID))

	startErr := cmd.Start()
	viaShell := false
	if errors.Is(startErr, unix.ENOEXEC) {
		viaShell = true
		// An APE's header is valid shell, not a format the kernel loads, so
		// execve returns ENOEXEC until the binary has assimilated itself into
		// the host format. A POSIX shell answers ENOEXEC by running the file as
		// a script, which is the whole reason an APE is runnable -- and it is
		// how a real agent reaches an APE, since a tool call is spawned through a
		// shell. Go's os/exec has no such fallback, so do it explicitly rather
		// than report a portability gap in this harness as a guard failure.
		//
		// This is the macOS path: there the arm64 APE boots through a compiled
		// loader and stays a polyglot, so every exec of it needs the shell. On
		// linux it assimilates to a native ELF on its earliest run, so the direct exec
		// above succeeds and this never fires.
		shell := exec.Command("/bin/sh", append([]string{"-c", `"$0" "$@"`}, args...)...)
		shell.Stdout = cmd.Stdout
		shell.Stderr = cmd.Stderr
		shell.Env = cmd.Env
		cmd = shell
		startErr = cmd.Start()
	}
	// Close our copy of the child's end the moment the fork/dup2 has happened,
	// the way opencode/Node's child_process really does -- keeping it open
	// until after Wait() would let a same-inode /proc scan "find" our own
	// leftover duplicate instead of genuinely resolving the child's peer via
	// SO_PEERCRED, silently passing a case the real plumbing would fail.
	childStdout.Close()
	// Drain the socket, which is what a real agent does with a tool call's
	// stdio: it reads. Discarding it instead makes the child's own account of
	// itself unrecoverable, and a socketpair's buffer is small enough that a
	// chatty child would eventually block on a reader that never arrives.
	var outBuf strings.Builder
	drained := make(chan struct{})
	go func() {
		io.Copy(&outBuf, readerEnd)
		close(drained)
	}()

	var runErr error
	if startErr != nil {
		runErr = startErr
	} else {
		runErr = cmd.Wait()
	}
	// EOF arrives when every write end is closed, which the child's exit did --
	// unless it left a grandchild holding an end, so the wait is bounded and the
	// close is what unblocks the read in that case.
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
	}
	readerEnd.Close()

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			fmt.Fprintln(os.Stderr, "exec:", runErr)
			os.Exit(2)
		}
	}

	fmt.Println("HARNESS_CHILD_EXIT=" + strconv.Itoa(exitCode))
	if strings.Contains(errBuf.String(), "refused to run") {
		fmt.Println("HARNESS_GUARD_REFUSED=true")
	} else {
		fmt.Println("HARNESS_GUARD_REFUSED=false")
	}
	fmt.Println("HARNESS_VIA_SHELL=" + strconv.FormatBool(viaShell))
	// Both of the child's streams, on ours so they travel with the verdict
	// above. The guard names what it resolved the socket's reader to be, and
	// that sentence is the difference between "the peer lookup failed" and "the
	// peer resolved to something unrecognized" -- rival bugs that look identical
	// from the verdict alone. A run that ends some other way, before the guard,
	// is equally invisible without them, and it says so on whichever stream it
	// got to soonest.
	echo("HARNESS_CHILD_STDERR: ", errBuf.String())
	echo("HARNESS_CHILD_STDOUT: ", outBuf.String())
}

// echo prints text as prefixed lines, and says so when there was
// none -- an empty stream is itself the finding when a child exits without
// explaining itself.
func echo(prefix, text string) {
	if text == "" {
		fmt.Println(prefix + "(empty)")
		return
	}
	for _, line := range strings.Split(strings.TrimRight(text, "\n"), "\n") {
		fmt.Println(prefix + line)
	}
}
