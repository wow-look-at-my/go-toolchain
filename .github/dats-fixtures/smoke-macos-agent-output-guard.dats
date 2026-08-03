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

  # TEMPORARY DIAGNOSTIC (4th round): round 3's "go 1.21 forces a fresh
  # bootstrap" theory was WRONG -- this round's `go 1.24` (matching the outer
  # pipeline's already-cached toolchain) still fails identically. Confirmed by
  # reading src/main.go: EnsureGoVersion() failing for ANY reason takes a
  # SEPARATE, non-guard path (DiscardBuildOutputs + logger.Error + exit 1)
  # that produces the exact same exit-code/deletion side effects the guard
  # does, without ever printing the guard's message -- so deletion+exit-1
  # matching was never proof the guard fired. This round captures the
  # process's actual stderr to a file and buckets its content into mutually
  # exclusive markers (empty/non-empty length, then substring categories);
  # asserting all of them as "must be present" means dats' failure report
  # lists exactly the ones that DIDN'T print, revealing by elimination which
  # branch the real run actually took. Remove once the real cause is found
  # and fixed.
  - desc: agent output guard names opencode and deletes the module's build outputs (native darwin)
    cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; printf "module example.com/stalebin\n\ngo 1.24\n" > go.mod; printf "package main\n\nfunc main() {}\n" > main.go; mkdir build; echo stale > build/stalebin; echo keep > build/checksums.txt; {outputs.gt} 2> {outputs.errfile}; rc=$?; content="$(cat {outputs.errfile})"; len=$(printf "%s" "$content" | wc -c); if [ "$len" -eq 0 ]; then echo MARK-LEN-ZERO; else echo MARK-LEN-NONZERO; fi; if printf "%s" "$content" | grep -q "refused to run"; then echo MARK-HAS-REFUSED; fi; if printf "%s" "$content" | grep -qi "go bootstrap"; then echo MARK-HAS-GO-BOOTSTRAP; fi; if printf "%s" "$content" | grep -qi "not found"; then echo MARK-HAS-NOT-FOUND; fi; if printf "%s" "$content" | grep -qi "panic"; then echo MARK-HAS-PANIC; fi; if printf "%s" "$content" | grep -qi "permission denied"; then echo MARK-HAS-PERM-DENIED; fi; if printf "%s" "$content" | grep -qi "no such file"; then echo MARK-HAS-NO-SUCH-FILE; fi; if printf "%s" "$content" | grep -qi "operation not permitted"; then echo MARK-HAS-OP-NOT-PERMITTED; fi; if printf "%s" "$content" | grep -qi "integrity probe"; then echo MARK-HAS-INTEGRITY-PROBE; fi; if [ ! -e build/stalebin ]; then echo GUARD-DELETED-BINARY; fi; if [ -f build/checksums.txt ]; then echo GUARD-KEPT-CHECKSUMS; fi; exit $rc'
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
        - "MARK-LEN-ZERO"
        - "MARK-LEN-NONZERO"
        - "MARK-HAS-REFUSED"
        - "MARK-HAS-GO-BOOTSTRAP"
        - "MARK-HAS-NOT-FOUND"
        - "MARK-HAS-PANIC"
        - "MARK-HAS-PERM-DENIED"
        - "MARK-HAS-NO-SUCH-FILE"
        - "MARK-HAS-OP-NOT-PERMITTED"
        - "MARK-HAS-INTEGRITY-PROBE"
