# Agent output guard regression on a macOS HOST -- see
# docs/AGENT-OUTPUT-GUARD.md. Copied by the smoke-macos job
# (.github/workflows/ci.yml) into a throwaway module's dats/ directory, not run
# against THIS repo's own dats/cli.dats: that suite runs during this repo's own
# linux self-build, which cannot exercise a darwin host.
#
# ./gt-under-test is a copy of the exact published APE, which is what ARM64
# macs download. Note which guard implementation that exercises: the APE
# reports runtime.GOOS == "cosmo", so it compiles the _cosmo sockpeer/tty
# classifiers, NOT claudeguard_darwin.go. Testing the binary consumers
# actually run is the point, and on macOS that is now the cosmo path.
#
# Scratch space is always
# `{outputs.X}` (dats' own writable per-test directory), never `mktemp -d`:
# seatbelt (macOS's sandbox backend) restricts writes to exactly that
# directory and nothing else, so a bare `mktemp -d` (which lands in the
# ambient $TMPDIR, a SIBLING of that directory, not inside it) is silently
# denied there -- unlike linux's bwrap, which privatizes the whole /tmp
# namespace and so tolerates it. `{outputs.X}` is documented to work
# identically across every sandbox backend (see dats' file-format.md).

sandbox:
	image: golang:1.25

tests:
	# Everything host-specific this binary does hangs off hostos.Detect(), whose
	# filesystem probes are reads of absolute paths and whose fallback is
	# "linux". Seatbelt is exactly the environment that can deny those reads, so
	# assert the answer HERE, inside the sandbox, not only from the unsandboxed
	# CI step. A "linux" answer on this runner would mean every dependent
	# decision -- including the output guard's whole classifier -- is silently
	# taking the wrong branch under the sandbox the guard tests run in.
	# What an ARM64 mac downloads IS the fat APE, not a native darwin build.
	- desc: the shipped artifact carries the APE magic
	  cmd: 'head -c 6 ./gt-under-test'
	  timeout: 30s
	  outputs:
		stdout:
			- "MZqFpD"

	- desc: the APE detects a darwin host from inside the sandbox
	  cmd: 'cp ./gt-under-test {outputs.gt}; {outputs.gt} version host'
	  timeout: 30s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "host: darwin"
		"!stdout":
			- "GUESSED"

	# The guard must CLASSIFY here, not fall back to announcing that it cannot
	# see. Both halves matter and they fail independently of each other: losing
	# the refusal means the guard stopped working, and gaining the INOPERATIVE
	# banner means it went blind and said so. A blind guard ALLOWS, so a
	# regression in the darwin classifier shows up as both at once.
	#
	# Stated as the inverse on purpose. This descriptor is a captured stdout,
	# which fstat alone classifies, so it stays decidable no matter what the
	# fork gains later -- unlike an assertion that the banner DOES fire, which
	# would describe a state that disappears the moment the socket probes land.
	- desc: the guard classifies on a macOS host rather than going blind
	  cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; env OPENCODE=1 {outputs.gt}; rc=$?; exit $rc'
	  exit: 1
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stderr:
			- "refused to run"
		"!stderr":
			- "INOPERATIVE"

	- desc: agent output guard refuses a captured pipeline run under {matrix.marker} (APE on a macOS host)
	  cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; env {matrix.marker}=1 {outputs.gt}; rc=$?; exit $rc'
	  exit: 1
	  timeout: 60s
	  matrix:
		marker: [GROK_AGENT, OPENCODE]
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stderr:
			- "refused to run"
		"!stdout":
			- "Build successful"

	- desc: version stays exempt under {matrix.marker} (APE on a macOS host)
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

	# EnsureGoVersion's version check runs `go list runtime` (verifyGoToolchain,
	# see src/cmd/gobootstrap.go) to probe the toolchain's integrity, and that
	# needs to write a GOCACHE entry. Under seatbelt only {outputs.X} is
	# writable, so without an explicit GOCACHE the probe fails with "package
	# runtime is not in std", taking main.go's non-guard bootstrap-error exit
	# path instead of the guard -- same exit code and build-output cleanup
	# (both paths discard build outputs on any unsuccessful run), but none of
	# the guard's own message. Point GOCACHE at the writable outputs dir so the
	# version check succeeds and control actually reaches the guard.
	- desc: agent output guard names opencode and deletes the module's build outputs (APE on a macOS host)
	  cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir} {outputs.gocache}; cd {outputs.rundir}; printf "module example.com/stalebin\n\ngo 1.24\n" > go.mod; printf "package main\n\nfunc main() {}\n" > main.go; mkdir build; echo stale > build/stalebin; echo keep > build/checksums.txt; {outputs.gt}; rc=$?; [ ! -e build/stalebin ] && echo GUARD-DELETED-BINARY; [ -f build/checksums.txt ] && echo GUARD-KEPT-CHECKSUMS; exit $rc'
	  exit: 1
	  timeout: 60s
	  inputs:
		env:
			OPENCODE: "1"
			GOCACHE: "{outputs.gocache}"
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "GUARD-DELETED-BINARY"
			- "GUARD-KEPT-CHECKSUMS"
		stderr:
			- "refused to run"
			- "opencode"
			- "have been DELETED"

	# dats/cli.dats proves --help against the dev build on a linux host; this
	# proves the APE's polyglot format loads and dispatches on a macOS host.
	# --help exits before the pipeline/dats phase (see docs/CI.md), so it
	# carries no agent marker and needs no bootstrap timing -- Go is already
	# cached from the pipeline run above. This test runs from the module root
	# (dats' default cwd), which has its own go.mod, so it hits the same
	# verifyGoToolchain/GOCACHE requirement as the deletion test above.
	- desc: the APE prints usage under --help on a macOS host
	  cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.gocache}; {outputs.gt} --help'
	  timeout: 30s
	  inputs:
		env:
			GOCACHE: "{outputs.gocache}"
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "Usage:"

	# ./socketharness-under-test reproduces a coding agent's own tool-execution
	# plumbing: it wires the binary's stdout through a socketpair (what a
	# Node/Bun child_process actually uses on macOS too, not a FIFO) and exports
	# OPENCODE_PID naming itself as the reader -- the real shape of the bug
	# report this fixture exists to catch (see docs/AGENT-OUTPUT-GUARD.md): a
	# completely unpiped, unredirected go-toolchain run under opencode on
	# macOS was refused as "captured instead of printed to the terminal"
	# because darwin's socket case failed closed unconditionally, with no peer
	# check at all -- unlike the FIFO case, a socket's peer pid is directly
	# available via getsockopt(LOCAL_PEERPID), no libproc needed. No go.mod in
	# {outputs.rundir} means EnsureGoVersion takes the already-cached-Go fast
	# path before ever reaching the version-integrity probe, so this does not
	# need the GOCACHE workaround the tests above do.
	- desc: agent output guard allows a plain run when the socket reader is the agent itself (APE on a macOS host)
	  cmd: 'cp ./socketharness-under-test {outputs.harness}; cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; {outputs.harness} {outputs.gt}'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "HARNESS_GUARD_REFUSED=false"

	# Naming a socket's peer on darwin means running ps(1): macOS has no /proc,
	# and the sysctl(KERN_PROC) a native darwin build would use answers ENOSYS
	# from a cosmo APE. Seatbelt refuses to execute it (MEASURED: exit 126),
	# which is why classifyDarwinFD falls back to the pid the agent published
	# rather than treating an unnameable peer as a capture -- the socket test
	# below is what that fallback buys. Pinned here because the guard's own
	# verdict cannot report it: an unrunnable ps and a real capture both come
	# out as a refusal. A red here means the sandbox gained ps, which makes the
	# fallback a second opinion rather than the only one; update this test, and
	# keep the fallback for the sandboxes that still refuse.
	- desc: ps stays unavailable inside the sandbox, which is what the pid fallback is for
	  cmd: '/bin/ps -o ppid=,ucomm= -p 1'
	  exit: 126
	  timeout: 30s
	  outputs:
		"!stdout":
			- "launchd"

	- desc: agent output guard still refuses a socket whose reader is not the agent (APE on a macOS host)
	  cmd: 'cp ./socketharness-under-test {outputs.harness}; cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; {outputs.harness} --wrong-reader {outputs.gt}'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "HARNESS_GUARD_REFUSED=true"
