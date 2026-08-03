# Agent output guard regression for the native darwin/arm64 binary -- see
# src/cmd/claudeguard_darwin.go and docs/AGENT-OUTPUT-GUARD.md. darwin has no
# /proc, so a GOOS=darwin unit test proves the classifier compiles but not
# that the real binary, executed on real darwin, actually refuses. This file
# is copied into a throwaway module's dats/ directory by the smoke-macos job
# (.github/workflows/ci.yml, "Full pipeline with the native darwin/arm64
# binary"), not run against THIS repo's own dats/cli.dats: that suite is
# linux/cosmo-only (see dats/README.md) and this repo's own self-build never
# runs on darwin.
#
# ./gt-under-test is a copy of the exact published darwin/arm64 binary,
# staged inside the module root by that CI step -- dats sandboxes every
# command to the module root, so a path under $RUNNER_TEMP would be
# invisible here (same reasoning as dats/README.md's build/.dats-stage/
# staging). Mirrors dats/cli.dats' own linux/cosmo guard tests 1:1, matrixed
# over the same two agents that always pipe a command's stdout back to
# themselves.

sandbox:
  image: golang:1.25

tests:
  - desc: agent output guard refuses a captured pipeline run under {matrix.marker} (native darwin)
    cmd: 'b="$(mktemp -d)"; cp ./gt-under-test "$b/gt"; d="$(mktemp -d)"; cd "$d"; env {matrix.marker}=1 "$b/gt"; rc=$?; cd /; rm -rf "$d" "$b"; exit $rc'
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
    cmd: 'd="$(mktemp -d)"; cp ./gt-under-test "$d/gt"; env {matrix.marker}=1 "$d/gt" version raw'
    timeout: 30s
    matrix:
      marker: [GROK_AGENT, OPENCODE]
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      "!stderr":
        - "refused to run"

  - desc: agent output guard names opencode and deletes the module's build outputs (native darwin)
    cmd: 'b="$(mktemp -d)"; cp ./gt-under-test "$b/gt"; d="$(mktemp -d)"; cd "$d"; printf "module example.com/stalebin\n\ngo 1.21\n" > go.mod; printf "package main\n\nfunc main() {}\n" > main.go; mkdir build; echo stale > build/stalebin; echo keep > build/checksums.txt; OPENCODE=1 "$b/gt"; rc=$?; [ ! -e build/stalebin ] && echo GUARD-DELETED-BINARY; [ -f build/checksums.txt ] && echo GUARD-KEPT-CHECKSUMS; cd /; rm -rf "$d" "$b"; exit $rc'
    exit: 1
    timeout: 60s
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "GUARD-DELETED-BINARY"
        - "GUARD-KEPT-CHECKSUMS"
      stderr:
        - "refused to run"
        - "opencode"
        - "have been DELETED"
