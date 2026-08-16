# Command-line tests for the go-toolchain binary, run automatically by the
# dats phase after every build (see dats/README.md for the conventions).
#
# $GO_TOOLCHAIN_DATS_BUILD_DIR holds throwaway copies of the binaries this
# pipeline just built. The dir is READ-ONLY inside the sandbox, and the binary
# under test is an APE on the linux slots -- one that used to exit 121 from a
# read-only path, because its loader rewrote its own file on first exec. The
# dats phase assimilates each copy as it stages it, so every test below execs
# the handoff path in place. Keep them that way: an in-place exec here is what
# catches a regression in that staging step.
# GO_TOOLCHAIN_BUILDHOST_URL points at an unreachable address on every test so
# the background update check fails instantly and silently, keeping output
# deterministic regardless of what buildhost has published.
#
# NOTE: the agent-output-guard tests below assume a linux host — this repo's
# own self-build (the only thing that runs THIS suite) never runs on darwin.
# A macOS host is covered by the sibling fixture
# .github/dats-fixtures/smoke-macos-agent-output-guard.dats, which the
# smoke-macos job copies into a throwaway module. It cannot live under this
# repo's own dats/: dats runs every suite it finds recursively there, so a
# suite asserting darwin-host behavior would also run (and fail) during this
# repo's own linux build/host-build jobs.

# Sandboxed like every other suite (dats' default). The one adjustment: under
# the docker backend the commands run in the IMAGE's filesystem, and every
# go-toolchain invocation past `version` bootstraps a Go toolchain — with no Go
# in the image it would download one per command. A Go-bearing image gives the
# bootstrap something to find. bwrap and seatbelt ignore `image` (they run on
# the host's own filesystem, where the pipeline's Go already is).
sandbox:
	image: golang:1.25

setup:
	# Sanity: the staged binary exists and execs where it stands. `version raw`
	# is the cheapest invocation — no Go bootstrap, no update check, no network.
	- test -x "$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain"
	- '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" version raw'

tests:
	# The only test here that reaches the staleness footer, whose commit queries
	# would otherwise ride api.github.com -- up to two round trips at a 10s
	# client timeout each, spent inside the second-build wall-clock budget
	# host-build enforces. Unreachable base = the offline footer, instantly.
	- desc: version reports the build stamp
	  cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" version'
	  timeout: 30s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
			GO_TOOLCHAIN_GITHUB_API_URL: "http://127.0.0.1:1"
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

	# Host detection, from inside the sandbox. hostos.Detect()'s filesystem
	# probes are reads of absolute paths and its fallback is "linux", so a
	# sandbox that denies them yields the right answer here for the WRONG
	# reason. Asserting the method, not just the answer, is what catches that:
	# under bwrap this must still be a measurement. The macOS fixture asserts
	# the darwin direction under seatbelt.
	- desc: host detection reports linux by measurement, not by fallback
	  cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" version host'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "host: linux"
		"!stdout":
			- "GUESSED"

	# The default matrix builds ONE multi-platform APE; --os/--arch opt into a
	# per-platform product. That is a promise made in --help, so pin it: the
	# platform-set flag has to exist with the documented default, and --os/--arch
	# must not carry defaults of their own (a default there would silently
	# restore the cartesian product as the default build).
	- desc: matrix --help documents the single-APE default
	  cmd: '"$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain" matrix --help'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "--cosmo-platforms"
			- "linux/amd64,darwin/arm64,windows/amd64"
		# The CLI cannot ask for a per-platform copy of the APE: there is no
		# flag, because there is no copier behind one.
		"!stdout":
			- "--cosmo-slots"
			- "(default [linux,darwin,windows])"
			- "(default [amd64,arm64])"


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

	# A directory with neither a module nor suites is the one case that still
	# refuses, and the message has to name both halves -- "no go.mod found" alone
	# sent people off to `go mod init` a shell repo that only wanted its suites
	# run.
	#
	# The POSITIVE case (suites present, no go.mod, they run) is a Go unit test,
	# not a case here: asserting it from a suite means go-toolchain starting dats
	# inside a command dats is already sandboxing, and nested bwrap is not a
	# thing worth depending on for coverage the unit tests already give.
	- desc: no module and no suites names both halves
	  cmd: 'd="$(mktemp -d)"; cd "$d"; "$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain"; rc=$?; cd /; rm -rf "$d"; exit $rc'
	  exit: 1
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
			# Every other full-pipeline test here SETS an agent marker, because it
			# is asserting the guard. This is the first that needs the guard OFF,
			# and the markers leak in from the host: inside a Claude Code session
			# CLAUDE_CODE_SESSION_ID alone makes the guard refuse before the module
			# check is ever reached, so the assertion would pass in CI and fail on
			# a developer's machine. Empty reads as not-an-agent (the detector
			# treats "" and "0" as unset).
			CLAUDECODE: ""
			CLAUDE_CODE_SESSION_ID: ""
			GROK_AGENT: ""
			OPENCODE: ""
			OPENCODE_PID: ""
			GEMINI_CLI: ""
			CODEX_SANDBOX: ""
			CODEX_SANDBOX_NETWORK_DISABLED: ""
	  outputs:
		stderr:
			- "no go.mod and no dats/ suites found"
