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

  # EnsureGoVersion's version check runs `go list runtime` (verifyGoToolchain,
  # see src/cmd/gobootstrap.go) to probe the toolchain's integrity, and that
  # needs to write a GOCACHE entry. Under seatbelt only {outputs.X} is
  # writable, so without an explicit GOCACHE the probe fails with "package
  # runtime is not in std", taking main.go's non-guard bootstrap-error exit
  # path instead of the guard -- same exit code and build-output cleanup
  # (both paths discard build outputs on any unsuccessful run), but none of
  # the guard's own message. Point GOCACHE at the writable outputs dir so the
  # version check succeeds and control actually reaches the guard.
  - desc: agent output guard names opencode and deletes the module's build outputs (native darwin)
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

  # dats/cli.dats proves --help against the dev build on linux/cosmo only;
  # this proves it against the actual published native darwin/arm64 binary.
  # --help exits before the pipeline/dats phase (see docs/CI.md), so it
  # carries no agent marker and needs no bootstrap timing -- Go is already
  # cached from the pipeline run above. This test runs from the module root
  # (dats' default cwd), which has its own go.mod, so it hits the same
  # verifyGoToolchain/GOCACHE requirement as the deletion test above.
  - desc: native darwin binary prints usage under --help
    cmd: 'cp ./gt-under-test {outputs.gt}; mkdir -p {outputs.gocache}; {outputs.gt} --help'
    timeout: 30s
    inputs:
      env:
        GOCACHE: "{outputs.gocache}"
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "Usage:"

  # The macOS carve-out ships a native Mach-O darwin/arm64 slot (tested
  # above), but the linux/cosmo slots are still the fat APE, and the APE's
  # polyglot format is ALSO a valid macOS executable -- ARM64 mac users who
  # grab a linux artifact by mistake, or a script that always fetches the
  # linux slot, still need it to run. ./gt-ape-under-test is a copy of that
  # same published binary, staged by the CI step alongside gt-under-test.
  # Only --help is exercised here, never the full pipeline: running the APE
  # slot through go-toolchain's full tidy/vet/test/build pipeline on a macOS
  # host hits a known exit-time deadlock in the cosmopolitan runtime (see
  # docs/CI.md) -- --help returns long before that pipeline ever starts.
  # Go is already cached from the earlier test in this file, same reasoning
  # as the native --help test above -- proving a truly cold, nothing-cached
  # bootstrap works is already covered there (bootstrapGo has no per-slot
  # branching, so a native-binary bootstrap proves the APE's too); this test
  # exists to prove the polyglot format itself loads and dispatches on macOS.
  - desc: APE (cosmo) binary prints usage under --help on a macOS host
    cmd: 'cp ./gt-ape-under-test {outputs.gt}; mkdir -p {outputs.gocache}; {outputs.gt} --help'
    timeout: 30s
    inputs:
      env:
        GOCACHE: "{outputs.gocache}"
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "Usage:"
