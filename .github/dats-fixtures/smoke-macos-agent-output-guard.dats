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

  # TEMPORARY DIAGNOSTIC (5th round): round 4 proved stderr is completely
  # EMPTY (MARK-LEN-ZERO printed, every stderr-content marker was absent) yet
  # the process still exits 1 and discards build outputs. src/logger/logger.go
  # explains it: Warn/Error route through EmitGHAError/EmitGHAWarning to
  # STDOUT (as a `::error::`/`::warning::` GHA annotation) whenever
  # GITHUB_ACTIONS=="true" -- which it is, inherited into the sandboxed
  # process same as every other env var. Only the agent-output guard's own
  # abort message bypasses the logger entirely (agentGuardOut is hardcoded to
  # os.Stderr, see claudeguard.go) to guarantee it survives redirection; every
  # other error path in this binary, including EnsureGoVersion's, does not.
  # So the real failure text was in STDOUT all along, mixed in with this
  # test's own echo markers, unassorted by any assertion. This round captures
  # gt-under-test's OWN stdout separately and checks it against every literal
  # message fragment gobootstrap.go can actually produce for this exact
  # scenario (go.mod present, required version already satisfies installed),
  # read directly from source rather than guessed. Remove once the real cause
  # is found and fixed.
  - desc: agent output guard names opencode and deletes the module's build outputs (native darwin)
    cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.rundir}; cd {outputs.rundir}; printf "module example.com/stalebin\n\ngo 1.24\n" > go.mod; printf "package main\n\nfunc main() {}\n" > main.go; mkdir build; echo stale > build/stalebin; echo keep > build/checksums.txt; {outputs.gt} > {outputs.outfile} 2> {outputs.errfile}; rc=$?; out="$(cat {outputs.outfile})"; found=0; if printf "%s" "$out" | grep -q "go-bootstrap: found go at"; then echo MARK-FOUND-GO; found=1; fi; if printf "%s" "$out" | grep -q "installed Go .* satisfies required"; then echo MARK-SATISFIES; found=1; fi; if printf "%s" "$out" | grep -q "is present but broken"; then echo MARK-PRESENT-BUT-BROKEN; found=1; fi; if printf "%s" "$out" | grep -q "bootstrapping Go"; then echo MARK-BOOTSTRAPPING; found=1; fi; if printf "%s" "$out" | grep -q "using Go .* from"; then echo MARK-USING-GO; found=1; fi; if printf "%s" "$out" | grep -q "failed integrity probe"; then echo MARK-FAILED-INTEGRITY-PROBE; found=1; fi; if printf "%s" "$out" | grep -q "go bootstrap: "; then echo MARK-GO-BOOTSTRAP-ERROR; found=1; fi; if printf "%s" "$out" | grep -q "::error::"; then echo MARK-GHA-ERROR-ANNOTATION; found=1; fi; if printf "%s" "$out" | grep -q "::warning::"; then echo MARK-GHA-WARNING-ANNOTATION; found=1; fi; if printf "%s" "$out" | grep -qi "permission denied"; then echo MARK-PERM-DENIED; found=1; fi; if printf "%s" "$out" | grep -qi "operation not permitted"; then echo MARK-OP-NOT-PERMITTED; found=1; fi; if printf "%s" "$out" | grep -qi "no such file"; then echo MARK-NO-SUCH-FILE; found=1; fi; if printf "%s" "$out" | grep -q "refused to run"; then echo MARK-GUARD-FIRED; found=1; fi; len=$(printf "%s" "$out" | wc -c); if [ "$len" -eq 0 ]; then echo MARK-OUT-EMPTY; found=1; fi; if [ "$found" -eq 0 ]; then b64=$(printf "%s" "$out" | tail -c 300 | base64 | tr -d "\n=+/"); echo "MARK-UNRECOGNIZED-$b64"; fi; if [ ! -e build/stalebin ]; then echo GUARD-DELETED-BINARY; fi; if [ -f build/checksums.txt ]; then echo GUARD-KEPT-CHECKSUMS; fi; exit $rc'
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
        - "MARK-FOUND-GO"
        - "MARK-SATISFIES"
        - "MARK-PRESENT-BUT-BROKEN"
        - "MARK-BOOTSTRAPPING"
        - "MARK-USING-GO"
        - "MARK-FAILED-INTEGRITY-PROBE"
        - "MARK-GO-BOOTSTRAP-ERROR"
        - "MARK-GHA-ERROR-ANNOTATION"
        - "MARK-GHA-WARNING-ANNOTATION"
        - "MARK-PERM-DENIED"
        - "MARK-OP-NOT-PERMITTED"
        - "MARK-NO-SUCH-FILE"
        - "MARK-GUARD-FIRED"
        - "MARK-OUT-EMPTY"
