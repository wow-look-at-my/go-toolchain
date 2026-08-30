//go:build cosmo

// FIFO peer identification for a cosmo APE on a darwin host. The APE has no
// proc_info syscall (cosmopolitan leaves it unwired), so this asks lsof the
// same way CommPPID asks the ps tool when the APE cannot use sysctl. A sandbox that
// refuses lsof is "not identified" and classifyDarwinFD fails closed.

package cmd

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"

	agent "github.com/wow-look-at-my/is-this-an-agent"
)

const (
	lsofBin         = "/usr/sbin/lsof"
	maxAncestorHops = 64

	// peerProbeBudget caps the whole walk. Every step of it shells out --
	// ps per ancestor hop, then lsof twice -- and none of those had a
	// deadline, so one helper that never returned hung the process before
	// main ran. The second lsof is the widest: no -d filter, so it lists
	// every open file of every ancestor, and lsof makes blocking lstat and
	// readlink calls to do it.
	//
	// Expiry reports "not identified", which classifyDarwinFD already
	// treats as a pipe whose far end it cannot see, so a slow host refuses
	// rather than proceeding blind. That is this path's existing rule.
	peerProbeBudget = 5 * time.Second
)

// fifoPeerOnDarwinHost returns the ancestor pid holding the other end of the
// FIFO at fd, by matching lsof pipe handles. supported is always true.
func fifoPeerOnDarwinHost(fd uintptr) (pid int, identified, supported bool) {
	ctx, cancel := context.WithTimeout(context.Background(), peerProbeBudget)
	defer cancel()

	_, peer, ok := lsofFDPipe(ctx, os.Getpid(), int(fd))
	if !ok {
		return 0, false, true
	}
	var ancestors []int
	p := os.Getppid()
	for hops := 0; p > 1 && hops < maxAncestorHops; hops++ {
		// A hop runs a ps carrying its own timeout but no WaitDelay, so the
		// budget is checked between hops rather than trusting a per-call
		// bound to bound the walk.
		if ctx.Err() != nil {
			return 0, false, true
		}
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
	handles := lsofPIDsPipeHandles(ctx, ancestors)
	for _, a := range ancestors {
		if _, ok := handles[a][peer]; ok {
			return a, true, true
		}
	}
	return 0, false, true
}

// lsofCommand builds an lsof invocation that cannot outlive ctx. WaitDelay
// matters as much as the deadline: killing the child still leaves Wait
// blocked until every holder of its output pipe closes it.
func lsofCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, lsofBin, args...)
	cmd.WaitDelay = time.Second
	return cmd
}

func lsofFDPipe(ctx context.Context, pid, fd int) (handle, peer uint64, ok bool) {
	out, err := lsofCommand(ctx, "-w", "-nP",
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

func lsofPIDsPipeHandles(ctx context.Context, pids []int) map[int]map[uint64]uint64 {
	if len(pids) == 0 {
		return nil
	}
	args := []string{"-w", "-nP", "-F", "pftnd", "-p", joinPids(pids)}
	out, err := lsofCommand(ctx, args...).Output()
	if err != nil {
		return nil
	}
	return parseLsofPipeHandles(string(out))
}
