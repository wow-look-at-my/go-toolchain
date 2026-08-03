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

  # TEMPORARY DIAGNOSTIC (3rd round): confirmed via round 2 -- $OPENCODE is
  # "1" in the shell before the exec, exit code is 1, the binary is deleted
  # and checksums kept, ALL matching the guard's expected side effects
  # exactly. Yet no stderr assertion for the guard's own message text has
  # ever matched. This round captures stderr to a file and tests fragments
  # of the expected message individually (not the whole phrase) to localize
  # exactly where matching breaks -- whole message, partial corruption, or
  # genuinely empty. Remove once the real cause is found and fixed.
  - desc: agent output guard names opencode and deletes the module's build outputs (native darwin)
    cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; printf "module example.com/stalebin\n\ngo 1.21\n" > go.mod; printf "package main\n\nfunc main() {}\n" > main.go; mkdir build; echo stale > build/stalebin; echo keep > build/checksums.txt; {outputs.gt} 2> {outputs.stderr.txt}; n=$(wc -c < {outputs.stderr.txt} | tr -d " "); if [ "$n" = "0" ]; then echo MARK_STDERR_ZERO_BYTES; elif [ "$n" -lt 50 ]; then echo MARK_STDERR_UNDER_50_BYTES; elif [ "$n" -lt 500 ]; then echo MARK_STDERR_50_TO_500_BYTES; else echo MARK_STDERR_OVER_500_BYTES; fi; grep -q "refused" {outputs.stderr.txt} && echo MARK_HAS_refused || echo MARK_NO_refused; grep -qi "opencode" {outputs.stderr.txt} && echo MARK_HAS_opencode || echo MARK_NO_opencode; grep -q "DELETED" {outputs.stderr.txt} && echo MARK_HAS_DELETED || echo MARK_NO_DELETED; grep -q "WARNING" {outputs.stderr.txt} && echo MARK_HAS_WARNING || echo MARK_NO_WARNING; grep -q "panic" {outputs.stderr.txt} && echo MARK_HAS_panic || echo MARK_NO_panic; grep -q "go-bootstrap" {outputs.stderr.txt} && echo MARK_HAS_bootstrap || echo MARK_NO_bootstrap; exit 0'
    exit: 0
    timeout: 60s
    inputs:
      env:
        OPENCODE: "1"
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "MARK_STDERR_ZERO_BYTES"
        - "MARK_STDERR_UNDER_50_BYTES"
        - "MARK_STDERR_50_TO_500_BYTES"
        - "MARK_STDERR_OVER_500_BYTES"
        - "MARK_HAS_refused"
        - "MARK_NO_refused"
        - "MARK_HAS_opencode"
        - "MARK_NO_opencode"
        - "MARK_HAS_DELETED"
        - "MARK_NO_DELETED"
        - "MARK_HAS_WARNING"
        - "MARK_NO_WARNING"
        - "MARK_HAS_panic"
        - "MARK_NO_panic"
        - "MARK_HAS_bootstrap"
        - "MARK_NO_bootstrap"
