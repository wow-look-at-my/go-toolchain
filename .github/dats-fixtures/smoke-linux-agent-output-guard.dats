# Agent output guard regression for the shipped linux/cosmo APE -- see
# src/cmd/claudeguard_proc.go and docs/AGENT-OUTPUT-GUARD.md. Copied by the
# smoke-linux job (.github/workflows/ci.yml) into a throwaway module's dats/
# directory, not run against THIS repo's own dats/cli.dats: that suite tests
# go-toolchain's own dev build, not the released artifact, and every suite
# under this repo's own dats/ also runs during this repo's linux self-build.
#
# ./gt-under-test is a copy of the exact published linux/amd64 (cosmo) binary,
# staged inside the module root by that CI step. Scratch space is always
# `{outputs.X}` (dats' own writable per-test directory), never `mktemp -d`:
# bare `mktemp -d` only works under linux's bwrap because it privatizes the
# whole /tmp namespace, and `{outputs.X}` is the one idiom documented to work
# identically on every sandbox backend (see the macOS sibling fixture, which
# needs it for real).

sandbox:
  image: golang:1.25

tests:
  - desc: agent output guard refuses a captured pipeline run under {matrix.marker} (shipped APE)
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

  - desc: version stays exempt under {matrix.marker} (shipped APE)
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

  # OPENCODE is set via inputs.env, not inline in cmd -- matching dats/cli.dats'
  # own "agent output guard deletes the module's build outputs" test, the
  # closest existing precedent for this exact shape (mkdir build; plant a
  # stale binary; exec bare). That one uses inputs.env for its marker too.
  - desc: agent output guard names opencode and deletes the module's build outputs (shipped APE)
    cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; printf "module example.com/stalebin\n\ngo 1.21\n" > go.mod; printf "package main\n\nfunc main() {}\n" > main.go; mkdir build; echo stale > build/stalebin; echo keep > build/checksums.txt; {outputs.gt}; rc=$?; [ ! -e build/stalebin ] && echo GUARD-DELETED-BINARY; [ -f build/checksums.txt ] && echo GUARD-KEPT-CHECKSUMS; exit $rc'
    exit: 1
    timeout: 60s
    inputs:
      env:
        OPENCODE: "1"
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "GUARD-DELETED-BINARY"
        - "GUARD-KEPT-CHECKSUMS"
      stderr:
        - "refused to run"
        - "opencode"
        - "have been DELETED"

  # The other three tests all hit the pipe allowance; CLAUDECODE discarding to
  # /dev/null exercises the DIFFERENT sinkDiscard path (what the original
  # hand-rolled bash step tested) so converting to dats does not silently
  # drop that coverage.
  - desc: agent output guard refuses a discarded run under CLAUDECODE (shipped APE)
    cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; {outputs.gt} > /dev/null 2> {outputs.err.txt}; rc=$?; cat {outputs.err.txt} >&2; exit $rc'
    exit: 1
    timeout: 60s
    inputs:
      env:
        CLAUDECODE: "1"
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stderr:
        - "refused to run"

  # dats/cli.dats already proves --help against the DEV build on an
  # unsandboxed system Go; this proves it against the actual shipped APE, the
  # artifact a consumer downloads. --help exits before the pipeline/dats phase
  # (see docs/CI.md), so it carries no agent marker and needs no bootstrap
  # timing -- Go is already cached from the pipeline run above.
  - desc: shipped APE prints usage under --help
    cmd: 'cp ./gt-under-test {outputs.gt}; {outputs.gt} --help'
    timeout: 30s
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "Usage:"

  # ./socketharness-under-test reproduces a coding agent's own tool-execution
  # plumbing: it wires the shipped APE's stdout through a socketpair (what a
  # Node/Bun child_process actually uses, not a bare pipe) and exports
  # OPENCODE_PID naming itself as the reader -- the real shape of the bug
  # report this fixture exists to catch (see docs/AGENT-OUTPUT-GUARD.md): a
  # completely unpiped, unredirected go-toolchain run was refused as
  # "captured instead of printed to the terminal" because sockets never got
  # the peer-identification chance a pipe gets.
  - desc: agent output guard allows a plain run when the socket reader is the agent itself
    cmd: 'cp ./socketharness-under-test {outputs.harness}; cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; {outputs.harness} {outputs.gt}'
    timeout: 60s
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "HARNESS_GUARD_REFUSED=false"

  - desc: agent output guard still refuses a socket whose reader is not the agent
    cmd: 'cp ./socketharness-under-test {outputs.harness}; cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; {outputs.harness} --wrong-reader {outputs.gt}'
    timeout: 60s
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "HARNESS_GUARD_REFUSED=true"
