# Agent output guard regression for the native darwin/arm64 binary -- see
# src/cmd/claudeguard_darwin.go and docs/AGENT-OUTPUT-GUARD.md. Copied by the
# smoke-macos job (.github/workflows/ci.yml) into a throwaway module's dats/
# directory, not run against THIS repo's own dats/cli.dats: that suite is
# linux/cosmo-only (see dats/README.md) and this repo's own self-build never
# runs on darwin.
#
# ./gt-under-test is a copy of the exact published darwin/arm64 binary, staged
# inside the module root by that CI step. Scratch space is always
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
  - desc: agent output guard refuses a captured pipeline run under {matrix.marker} (native darwin)
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

  - desc: version stays exempt under {matrix.marker} (native darwin)
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

  # TEMPORARY DIAGNOSTIC (2nd round): inputs.env alone (matching cli.dats'
  # own precedent for this shape) still didn't fix it -- tests 1-2 (matrixed,
  # inline `env VAR=1 {outputs.gt}`) pass; this one (unmatrixed, mkdir
  # build + planted files before the exec) still shows no "refused to
  # run"/"opencode"/"have been DELETED" even though the binary still gets
  # deleted. PRECHECK proves whether the shell itself even sees $OPENCODE
  # before gt runs, separating "env not propagated" from "gt itself didn't
  # detect it". Remove once the real cause is found and fixed.
  - desc: agent output guard names opencode and deletes the module's build outputs (native darwin)
    cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; printf "module example.com/stalebin\n\ngo 1.21\n" > go.mod; printf "package main\n\nfunc main() {}\n" > main.go; mkdir build; echo stale > build/stalebin; echo keep > build/checksums.txt; echo "MARK_PRECHECK_OPENCODE_IS_${OPENCODE:-UNSET}"; {outputs.gt}; rc=$?; echo "MARK_RC_$rc"; [ ! -e build/stalebin ] && echo MARK_BINARY_DELETED || echo MARK_BINARY_KEPT; [ -f build/checksums.txt ] && echo MARK_CHECKSUMS_KEPT || echo MARK_CHECKSUMS_GONE; exit 0'
    exit: 0
    timeout: 60s
    inputs:
      env:
        OPENCODE: "1"
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "MARK_PRECHECK_OPENCODE_IS_1"
        - "MARK_PRECHECK_OPENCODE_IS_UNSET"
        - "MARK_RC_1"
        - "MARK_RC_0"
        - "MARK_BINARY_DELETED"
        - "MARK_BINARY_KEPT"
        - "MARK_CHECKSUMS_KEPT"
        - "MARK_CHECKSUMS_GONE"
