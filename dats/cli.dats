# Command-line tests for the go-toolchain binary, run automatically by the
# dats phase after every build (see dats/README.md for the conventions).
#
# $GO_TOOLCHAIN_DATS_BUILD_DIR holds throwaway copies of the binaries this
# pipeline just built; every test execs the freshly built go-toolchain.
# GO_TOOLCHAIN_BUILDHOST_URL points at an unreachable address on every test so
# the background update check fails instantly and silently, keeping output
# deterministic regardless of what buildhost has published.
#
# NOTE: the claude-output-guard tests assume a linux host — the guard
# classifier is compiled for linux||cosmo and is a documented no-op on native
# darwin/windows (the same scoping as the smoke-linux guard gate in CI).

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
  - desc: version stays exempt from the claude output guard
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
  - desc: claude output guard refuses a captured pipeline run
    cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain"'
    exit: 1
    timeout: 60s
    inputs:
      env:
        CLAUDECODE: "1"
        GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
        GO_TOOLCHAIN_NO_DEP_SUBMISSION: "1"
    outputs:
      stderr:
        - "refused to run"
      "!stdout":
        - "Build successful"

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

  - desc: unknown flag is rejected
    cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" --definitely-not-a-flag'
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
