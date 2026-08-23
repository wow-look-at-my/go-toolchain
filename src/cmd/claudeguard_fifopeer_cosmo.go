//go:build cosmo

// FIFO peer identification for a cosmo APE on a darwin host. The APE has no
// proc_info syscall (cosmopolitan leaves it unwired), so this asks lsof the
// same way CommPPID asks ps(1) when the APE cannot use sysctl. A sandbox that
// refuses lsof is "not identified" and classifyDarwinFD fails closed.

package cmd

import (
	"os"
	"os/exec"
	"strconv"

	agent "github.com/wow-look-at-my/is-this-an-agent"
)

const (
	lsofBin         = "/usr/sbin/lsof"
	maxAncestorHops = 64
)

// fifoPeerOnDarwinHost returns the ancestor pid holding the other end of the
// FIFO at fd, by matching lsof pipe handles. supported is always true.
func fifoPeerOnDarwinHost(fd uintptr) (pid int, identified, supported bool) {
	_, peer, ok := lsofFDPipe(os.Getpid(), int(fd))
	if !ok {
		return 0, false, true
	}
	var ancestors []int
	p := os.Getppid()
	for hops := 0; p > 1 && hops < maxAncestorHops; hops++ {
		ancestors = append(ancestors, p)
		_, ppid, ok := agent.CommPPID(p)
		if !ok {
			break
		}
		p = ppid
	}
	if len(ancestors) == 0 {
		return 0, false, true
	}
	handles := lsofPIDsPipeHandles(ancestors)
	for _, a := range ancestors {
		if _, ok := handles[a][peer]; ok {
			return a, true, true
		}
	}
	return 0, false, true
}

func lsofFDPipe(pid, fd int) (handle, peer uint64, ok bool) {
	out, err := exec.Command(lsofBin, "-w", "-nP",
		"-a", "-p", strconv.Itoa(pid), "-d", strconv.Itoa(fd),
		"-F", "pftnd").Output()
	if err != nil {
		return 0, 0, false
	}
	byPID := parseLsofPipeHandles(string(out))
	hs, ok := byPID[pid]
	if !ok || len(hs) == 0 {
		return 0, 0, false
	}
	for h, p := range hs {
		return h, p, true
	}
	return 0, 0, false
}

func lsofPIDsPipeHandles(pids []int) map[int]map[uint64]uint64 {
	if len(pids) == 0 {
		return nil
	}
	args := []string{"-w", "-nP", "-F", "pftnd", "-p", joinPids(pids)}
	out, err := exec.Command(lsofBin, args...).Output()
	if err != nil {
		return nil
	}
	return parseLsofPipeHandles(string(out))
}
