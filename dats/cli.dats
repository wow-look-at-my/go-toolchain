# Command-line tests for the go-toolchain binary, run automatically by the
# dats phase after every build (see dats/README.md for the conventions).
#
# $GO_TOOLCHAIN_DATS_BUILD_DIR holds throwaway copies of the binaries this
# pipeline just built; every test execs the freshly built go-toolchain.
# GO_TOOLCHAIN_BUILDHOST_URL points at an unreachable address on every test so
# the background update check fails instantly and silently, keeping output
# deterministic regardless of what buildhost has published.
#
# NOTE: the agent-output-guard tests assume a linux host — the guard
# classifier is compiled for linux||cosmo and is a documented no-op on native
# darwin/windows (the same scoping as the smoke-linux guard gate in CI).

# Sandboxed like every other suite (dats' default). The one adjustment: under
# the docker backend the commands run in the IMAGE's filesystem, and every
# go-toolchain invocation past `version` bootstraps a Go toolchain — with no Go
# in the image it would download one per command. A Go-bearing image gives the
# bootstrap something to find. bwrap and seatbelt ignore `image` (they run on
# the host's own filesystem, where the pipeline's Go already is).
sandbox:
  image: golang:1.25

setup:
  # Sanity: the staged binary exists and executes. `version raw` is the
  # cheapest invocation — no Go bootstrap, no update check, no network.
  - test -x "$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain"
  - '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" version raw'

tests:
  - desc: version reports the build stamp
    cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" version'
    timeout: 30s
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "Version:"
        - "Commit:"
      "!stderr":
        - "panic"

  # `version raw` skips the staleness footer's GitHub query entirely, so this
  # exemption test is fully offline (the whole version subtree is exempt).
  - desc: version stays exempt from the agent output guard
    cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" version raw'
    timeout: 30s
    inputs:
      env:
        CLAUDECODE: "1"
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      "!stderr":
        - "refused to run"

  # The guard-positive case: a bare pipeline run under Claude with captured
  # stdout (dats always captures) must refuse to run before doing any work.
  # CLAUDECODE=1 also guarantees the pipeline can never actually start here,
  # so this test never recurses into a nested build.
  #
  # Run from an EMPTY throwaway directory, not the module root: a guard abort
  # deletes the module's build outputs (src/cmd/staleoutputs.go), which here
  # would delete the very binaries this pipeline just built. With no go.mod
  # there are no targets to delete, so this stays a pure guard assertion —
  # the deletion itself is asserted by the next test.
  - desc: agent output guard refuses a captured pipeline run
    cmd: 'd="$(mktemp -d)"; cd "$d"; "$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain"; rc=$?; cd /; rm -rf "$d"; exit $rc'
    exit: 1
    timeout: 60s
    inputs:
      env:
        CLAUDECODE: "1"
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stderr:
        - "refused to run"
      "!stdout":
        - "Build successful"

  # Refusing to run is not enough on its own: the invocation that hides the
  # output typically ignores the exit code too, and a binary left at
  # build/<target> by an earlier run would be executed as proof of a build
  # that never happened. The abort must delete it (src/cmd/staleoutputs.go)
  # and say so, while leaving non-binary outputs alone. A throwaway module
  # with a planted binary: the guard aborts long before anything is compiled.
  - desc: agent output guard deletes the module's build outputs
    cmd: 'd="$(mktemp -d)"; cd "$d"; printf "module example.com/stalebin\n\ngo 1.21\n" > go.mod; printf "package main\n\nfunc main() {}\n" > main.go; mkdir build; echo stale > build/stalebin; echo keep > build/checksums.txt; "$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain"; rc=$?; [ ! -e build/stalebin ] && echo GUARD-DELETED-BINARY; [ -f build/checksums.txt ] && echo GUARD-KEPT-CHECKSUMS; cd /; rm -rf "$d"; exit $rc'
    exit: 1
    timeout: 60s
    inputs:
      env:
        CLAUDECODE: "1"
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "GUARD-DELETED-BINARY"
        - "GUARD-KEPT-CHECKSUMS"
      stderr:
        - "refused to run"
        - "have been DELETED"

  - desc: root help prints usage
    cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" --help'
    timeout: 60s
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "Usage:"
        - "matrix"
        - "bench"
        - "lint"

  # The background update check is documented as silent on any error: with an
  # unreachable buildhost, no staleness warning may appear on either stream
  # (locally the warning goes to stderr; in GitHub Actions it becomes a
  # ::warning annotation on stdout).
  - desc: update check is silent when buildhost is unreachable
    cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" --help'
    timeout: 60s
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "Usage:"
      "!stdout":
        - "out of date"
      "!stderr":
        - "out of date"

  - desc: subcommand help
    cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" {matrix.sub} --help'
    timeout: 60s
    matrix:
      sub: [matrix, bench, lint, release, version]
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stdout:
        - "Usage:"

  # From a throwaway directory, not the module root: in the module root the
  # binary bootstraps the Go version go.mod demands, and a bootstrap that has
  # to download prints progress to stderr -- straight into the snapshot.
  - desc: unknown flag is rejected
    cmd: 'd="$(mktemp -d)"; cd "$d"; "$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" --definitely-not-a-flag; rc=$?; cd /; rm -rf "$d"; exit $rc'
    exit: 1
    timeout: 60s
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stderr:
        - "unknown flag"
      # Golden-file assertion: the full stderr must byte-match the committed
      # snapshot (regenerate with `dats --update test dats` after intentional
      # CLI changes).
      snapshot:
        stderr: true

  - desc: unknown subcommand is rejected
    cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" definitely-not-a-subcommand'
    exit: 1
    timeout: 60s
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      stderr:
        - "unknown command"

  # The guard covers every agent on the roster, each detected by its own
  # environment marker: grok build (GROK_AGENT) and opencode (OPENCODE). Both
  # pipe a command's stdout back to themselves, exactly as dats captures here.
  # These tests live at the END of the file: their position fixes the snapshot
  # test's index, which names the committed golden file above.
  #
  # The message's agent NAME is asserted by unit tests, not here: process
  # ancestry outranks the env marker, so running this suite from inside a
  # different agent's session would legitimately name that agent instead.
  - desc: agent output guard refuses a captured pipeline run under {matrix.marker}
    cmd: 'd="$(mktemp -d)"; cd "$d"; env {matrix.marker}=1 "$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain"; rc=$?; cd /; rm -rf "$d"; exit $rc'
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

  # version stays exempt under every agent, not only Claude.
  - desc: version stays exempt under {matrix.marker}
    cmd: 'env {matrix.marker}=1 "$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" version raw'
    timeout: 30s
    matrix:
      marker: [GROK_AGENT, OPENCODE]
    inputs:
      env:
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
    outputs:
      "!stderr":
        - "refused to run"
