# Command-line tests for the go-toolchain binary, run automatically by the
# dats phase after every build (see dats/README.md for the conventions).
#
# $GO_TOOLCHAIN_DATS_BUILD_DIR holds throwaway copies of the binaries this
# pipeline just built. It is READ-ONLY inside the sandbox (it lives under the
# working directory), and the binary under test may be an APE, whose loader
# rewrites its own file as it starts and exits 121 from a read-only path. So
# setup copies it to `{shared.gt.exe}` and every test execs that copy; a test
# needing a directory to work in makes its own `{outputs.mod}`.
#
# Scratch space is ALWAYS a dats placeholder, never `mktemp -d`. `{shared.X}`
# is the only namespace that expands in a setup command, and it expands in test
# commands too, so the same copy serves both. Only dats' own directories are
# writable under every backend. `mktemp -d` lands in the
# ambient temp directory, which bwrap tolerates because it privatizes the whole
# /tmp namespace -- and which seatbelt, macOS's backend, denies: `mkdtemp
# failed ... Operation not permitted`, leaving $d empty so every command runs
# against /gt. The sibling fixture under .github/dats-fixtures/ carries the
# same rule; dats' own docs/file-format.md is where it is documented.
# GO_TOOLCHAIN_BUILDHOST_URL points at an unreachable address on every test so
# the background update check fails instantly and silently, keeping output
# deterministic regardless of what buildhost has published.
#
# NOTE: build-everywhere self-builds this repo on every host, so every test
# here runs on linux, darwin and windows. Nothing below may name a host.

# Sandboxed like every other suite (dats' default). The adjustment: under
# the docker backend the commands run in the IMAGE's filesystem, and every
# go-toolchain invocation past `version` bootstraps a Go toolchain — with no Go
# in the image it would download a toolchain per command. A Go-bearing image gives the
# bootstrap something to find. bwrap and seatbelt ignore `image` (they run on
# the host's own filesystem, where the pipeline's Go already is).
sandbox:
	image: golang:1.25

setup:
	# Sanity: the staged binary exists and executes from a writable copy.
	# `version raw` is the cheapest invocation — no Go bootstrap, no update
	# check, no network. The staged name carries .exe on a windows host
	# (datsArtifactName), so the source is resolved rather than spelled, and the
	# copy always lands under .exe: NT needs the suffix to exec it and a posix
	# host does not care, which keeps the same name working everywhere.
	- 'src="$GO_TOOLCHAIN_DATS_BUILD_DIR/go-toolchain"; [ -x "$src" ] || src="$src.exe"; test -x "$src"; cp "$src" {shared.gt.exe}; {shared.gt.exe} version raw'

tests:
	# The only test here that reaches the staleness footer, whose commit queries
	# would otherwise ride api.github.com -- a round trip per commit at a 10s
	# client timeout each, spent inside the rebuild wall-clock budget
	# host-build enforces. Unreachable base = the offline footer, instantly.
	- desc: version reports the build stamp
	  cmd: '{shared.gt.exe} version'
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

	- desc: root help prints usage
	  cmd: '{shared.gt.exe} --help'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "Build Go projects with coverage enforcement"
			- "Usage:"
			- "matrix"
			- "bench"
			- "lint"

	# The background update check is documented as silent on any error: with an
	# unreachable buildhost, no staleness warning may appear on either stream
	# (locally the warning goes to stderr; in GitHub Actions it becomes a
	# ::warning annotation on stdout).
	- desc: update check is silent when buildhost is unreachable
	  cmd: '{shared.gt.exe} --help'
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
	  cmd: '{shared.gt.exe} {matrix.sub} --help'
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
	#
	# The byte-exact snapshot assertion below relies on logx's minDurationToShow
	# threshold: this error prints instantly during flag parsing (no I/O), well
	# under the 1s floor, so logx never appends a timing suffix and the golden
	# stays stable. If logx's threshold ever drops low enough for this line to
	# get timed, this assertion is what goes red soonest.
	- desc: unknown flag is rejected
	  cmd: 'mkdir -p {outputs.mod}; cd {outputs.mod}; {shared.gt.exe} --definitely-not-a-flag'
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
	  cmd: '{shared.gt.exe} definitely-not-a-subcommand'
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
	# reason. So this asserts the METHOD: the APE answers from the runtime's
	# own __hostos, ahead of every probe, and never from the guess. Which OS
	# each host reports is pinned per host by the smoke jobs, and this suite
	# runs on every host -- naming any of them here would fail on the others.
	- desc: host detection is a runtime measurement, never the fallback guess
	  cmd: '{shared.gt.exe} version host'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "(via runtime)"
		"!stdout":
			- "GUESSED"

	# The matrix builds a SINGLE multi-platform APE, and --help promises it. Pin the
	# promise: the platform-set flag exists with the documented default, and no
	# --os/--arch flag exists to silently reintroduce a cartesian product.
	- desc: matrix --help documents the single-APE default
	  cmd: '{shared.gt.exe} matrix --help'
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "--cosmo-platforms"
			- "linux/amd64,darwin/arm64,windows/amd64"
		# The CLI cannot ask for a per-platform copy of the APE: there is no
		# flag, because there is no copier behind such a flag.
		"!stdout":
			- "--cosmo-slots"
			- "--os "
			- "--arch "


	# A directory with neither a module nor suites is the case that still
	# refuses, and the message has to name both halves -- "no go.mod found" alone
	# sent people off to `go mod init` a shell repo that only wanted its suites
	# run.
	#
	# The POSITIVE case (suites present, no go.mod, they run) is a Go unit test,
	# not a case here: asserting it from a suite means go-toolchain starting dats
	# inside a command dats is already sandboxing, and nested bwrap is not a
	# thing worth depending on for coverage the unit tests already give.
	- desc: no module and no suites names both halves
	  cmd: 'mkdir -p {outputs.mod}; cd {outputs.mod}; {shared.gt.exe}'
	  exit: 1
	  timeout: 60s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stderr:
			- "no go.mod and no dats/ suites found"

	# The whole point of the APE, end to end: there is no spelling of --targets
	# that asks for a per-platform native binary, and the refusal arrives before
	# a toolchain is fetched to build it (the fork download would blow the
	# timeout and mask what is being asserted).
	- desc: --targets refuses a native platform
	  cmd: '{shared.gt.exe} matrix --targets {matrix.target}'
	  exit: 1
	  timeout: 30s
	  matrix:
		target: [linux/amd64, darwin/arm64, windows/amd64, cosmo/amd64]
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
	  outputs:
		stderr:
			- "invalid target"
		"!stderr":
			- "cosmo-bootstrap"

	# The pipeline is all or nothing: the go command and the build tools it
	# links answer only a process a pipeline run started. From a shell they
	# are not commands.
	- desc: the linked go command is not a command outside a pipeline run
	  cmd: '{shared.gt.exe} {matrix.args}'
	  exit: 1
	  timeout: 30s
	  matrix:
		args: ["go version", "go build ./...", "go test ./...", "tool compile -V=full"]
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
			GO_TOOLCHAIN_LINKED_GO: ""
	  outputs:
		stderr:
			- "unknown command"

	# The pipeline that built this binary put the fork checkout at its branch
	# head and stamped that commit in; version has to name the same commit.
	- desc: version names the gosmopolitan commit the build linked
	  cmd: 'test -n "$GO_TOOLCHAIN_DATS_GOSMOPOLITAN"; {shared.gt.exe} version | grep -F "Gosmopolitan: $GO_TOOLCHAIN_DATS_GOSMOPOLITAN"'
	  timeout: 30s
	  inputs:
		env:
			GO_TOOLCHAIN_BUILDHOST_URL: "http://127.0.0.1:1"
			GO_TOOLCHAIN_GITHUB_API_URL: "http://127.0.0.1:1"
	  outputs:
		stdout:
			- "Gosmopolitan: "
		"!stdout":
			- "Gosmopolitan: unknown"
