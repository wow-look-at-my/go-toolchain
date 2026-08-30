# Agent output guard regression for the SHIPPED APE -- see
# src/cmd/claudeguard_proc.go and docs/AGENT-OUTPUT-GUARD.md. smoke.dats copies
# this into a throwaway module's dats/ directory, so go-toolchain's own dats
# phase runs it against the artifact a consumer downloads. It is not part of
# this repo's own dats/cli.dats, which tests the dev build on the one host that
# builds it.
#
# ONE file for every host. What differs is asserted by pairing the answer with
# `uname -s` in the same line, never by a per-host copy of the file. Note which
# host that reports: this fixture runs INSIDE the sandbox, so a docker backend
# makes it Linux whatever the machine outside is, and seatbelt leaves it Darwin.
#
# ./gt-under-test is a copy of the published APE, staged in the module root.
# Scratch space is always `{outputs.X}`, dats' own writable per-test directory,
# never `mktemp -d`: seatbelt allows writes to exactly that directory, and a
# bare mktemp lands in a SIBLING of it and is silently denied. bwrap tolerates
# mktemp only because it privatizes the whole /tmp namespace.
#
# GOCACHE points into {outputs.X} for the same reason: EnsureGoVersion probes
# the toolchain with `go list runtime` (verifyGoToolchain, src/cmd/gobootstrap.go),
# which writes a cache entry. Without a writable GOCACHE that probe fails under
# seatbelt with "package runtime is not in std", and the run takes the
# bootstrap-error exit path -- same exit code, none of the guard's own message.

sandbox:
	image: golang:1.25

tests:
	# What was downloaded IS the fat APE. A native ELF would run here and pass
	# every other test in this file while shipping nothing a mac or a windows
	# box can start.
	- desc: the shipped artifact carries the APE magic
	  cmd: 'head -c 6 ./gt-under-test'
	  timeout: 30s
	  outputs:
		stdout:
			- "MZqFpD"

	# Everything host-specific this binary does hangs off hostos.Detect(), whose
	# filesystem probes are reads of absolute paths and whose fallback is
	# "linux". A sandbox is exactly the environment that can deny those reads,
	# so the answer is asserted HERE, inside it, not only from the CI step
	# outside. GUESSED means the probes failed and the fallback answered.
	- desc: the APE detects this host by measurement, and names the host the shell names
	  cmd: 'cp ./gt-under-test {outputs.gt}; printf "%s|%s\n" "$({outputs.gt} version host | head -1)" "$(uname -s)"'
	  timeout: 30s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			0: "^host: (linux.*\\|Linux|darwin.*\\|Darwin)"
		"!stdout":
			- "GUESSED"

	# The guard must CLASSIFY here, not fall back to announcing that it cannot
	# see. Both halves fail independently: losing the refusal means the guard
	# stopped working, and gaining the INOPERATIVE banner means it went blind
	# and said so. A blind guard ALLOWS, so a classifier regression shows up as
	# both at once. This descriptor is a captured stdout, which fstat alone
	# classifies, so it stays decidable whatever the fork gains later.
	- desc: the guard classifies on this host rather than going blind
	  cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir} {outputs.gocache}; cd {outputs.rundir}; env OPENCODE=1 {outputs.gt}; rc=$?; exit $rc'
	  exit: 1
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
			GOCACHE: "{outputs.gocache}"
	  outputs:
		stderr:
			- "refused to run"
		"!stderr":
			- "INOPERATIVE"

	- desc: agent output guard refuses a captured pipeline run under {matrix.marker}
	  cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir} {outputs.gocache}; cd {outputs.rundir}; env {matrix.marker}=1 {outputs.gt}; rc=$?; exit $rc'
	  exit: 1
	  timeout: 60s
	  matrix:
		marker: [GROK_AGENT, OPENCODE]
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
			GOCACHE: "{outputs.gocache}"
	  outputs:
		stderr:
			- "refused to run"
		"!stdout":
			- "Build successful"

	# version prints build metadata and no build result, so it is exempt from
	# the guard along with cacheprog: a captured run under an agent answers.
	- desc: version answers under {matrix.marker}
	  cmd: 'cp ./gt-under-test {outputs.gt}; env {matrix.marker}=1 {outputs.gt} version raw'
	  timeout: 30s
	  matrix:
		marker: [GROK_AGENT, OPENCODE]
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		"!stderr":
			- "refused to run"

	- desc: agent output guard names opencode and deletes the module's build outputs
	  cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir} {outputs.gocache}; cd {outputs.rundir}; printf "module example.com/stalebin\n\ngo 1.24\n" > go.mod; printf "package main\n\nfunc main() {}\n" > main.go; mkdir build; echo stale > build/stalebin; echo keep > build/checksums.txt; {outputs.gt}; rc=$?; [ ! -e build/stalebin ] && echo GUARD-DELETED-BINARY; [ -f build/checksums.txt ] && echo GUARD-KEPT-CHECKSUMS; exit $rc'
	  exit: 1
	  timeout: 60s
	  inputs:
		env:
			OPENCODE: "1"
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
			GOCACHE: "{outputs.gocache}"
	  outputs:
		stdout:
			- "GUARD-DELETED-BINARY"
			- "GUARD-KEPT-CHECKSUMS"
		stderr:
			- "refused to run"
			- "opencode"
			- "have been DELETED"

	# The other guard tests all hit the pipe allowance; CLAUDECODE discarding to
	# /dev/null exercises the DIFFERENT sinkDiscard path.
	- desc: agent output guard refuses a discarded run under CLAUDECODE
	  cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir} {outputs.gocache}; cd {outputs.rundir}; {outputs.gt} > /dev/null 2> {outputs.err.txt}; rc=$?; cat {outputs.err.txt} >&2; exit $rc'
	  exit: 1
	  timeout: 60s
	  inputs:
		env:
			CLAUDECODE: "1"
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
			GOCACHE: "{outputs.gocache}"
	  outputs:
		stderr:
			- "refused to run"

	# dats/cli.dats proves --help against the DEV build; this proves the APE's
	# polyglot format loads and dispatches wherever this fixture runs. --help
	# exits before the pipeline phase, so it carries no agent marker.
	- desc: the shipped APE prints usage under --help
	  cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.gocache}; {outputs.gt} --help'
	  timeout: 30s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
			GOCACHE: "{outputs.gocache}"
	  outputs:
		stdout:
			- "Usage:"

	# Naming a socket's peer on darwin means running ps(1): macOS has no /proc,
	# and the sysctl(KERN_PROC) a native darwin build would use answers ENOSYS
	# from a cosmo APE. Seatbelt refuses to execute ps, which is why
	# classifyDarwinFD falls back to the pid the agent published rather than
	# reading an unnameable peer as a capture -- the socket tests below are what
	# that fallback buys. The guard's own verdict cannot report this: an
	# unrunnable ps and a real capture both come out as a refusal. The line
	# names which branch ran, so a sandbox that GAINS ps shows up as a change
	# here rather than as silence.
	- desc: whether ps can name a process is what the darwin pid fallback turns on
	  cmd: 'if /bin/ps -o ppid= -p 1 >/dev/null 2>&1; then printf "%s|ps-available\n" "$(uname -s)"; else printf "%s|ps-blocked\n" "$(uname -s)"; fi'
	  timeout: 30s
	  outputs:
		stdout:
			0: "^(Linux\\|ps-(available|blocked)|Darwin\\|ps-blocked)$"

	# socketharness reproduces a coding agent's own tool-execution plumbing: it
	# wires the APE's stdout through a socketpair (what a Node or Bun
	# child_process actually uses, not a bare pipe) and exports OPENCODE_PID
	# naming itself as the reader -- the shape of the bug report this fixture
	# exists to catch (docs/AGENT-OUTPUT-GUARD.md): an unpiped, unredirected run
	# was refused as captured because a socket never got the peer-identification
	# chance a pipe gets. One harness per platform ships; the line picks this
	# host's, so the file stays the same everywhere.
	- desc: agent output guard allows a plain run when the socket reader is the agent itself
	  cmd: 'if [ "$(uname -s)" = "Darwin" ]; then cp ./socketharness-darwin {outputs.harness}; else cp ./socketharness-linux {outputs.harness}; fi; cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; {outputs.harness} {outputs.gt}'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "HARNESS_GUARD_REFUSED=false"

	- desc: agent output guard still refuses a socket whose reader is not the agent
	  cmd: 'if [ "$(uname -s)" = "Darwin" ]; then cp ./socketharness-darwin {outputs.harness}; else cp ./socketharness-linux {outputs.harness}; fi; cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; {outputs.harness} --wrong-reader {outputs.gt}'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "HARNESS_GUARD_REFUSED=true"
