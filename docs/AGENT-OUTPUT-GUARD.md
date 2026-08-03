# Agent output guard

`src/cmd/claudeguard.go` (+ `claudeguard_proc.go` / `claudeguard_darwin.go` /
`claudeguard_tty_linux.go` / `claudeguard_tty_cosmo.go` / `claudeguard_other.go`)
implements the **agent output guard**: the root `PersistentPreRunE` calls
`guardAgainstAgentOutputCapture()` (after the `skipCache` early-return, so
`cacheprog`/`version`/`install`/`release` are exempt — `cacheprog` in particular
*must* keep its stdout pipe for the GOCACHEPROG protocol) and aborts with exit 1
when go-toolchain runs under an AI coding agent **and** its stdout is anything
other than the harness transcript or a terminal — i.e. any pipe, a `> file` /
`>> file` redirect, `/dev/null`, or a `$(...)` capture.

## The agent roster

The roster is NOT here. Which agents exist, what environment markers they
export, what process names they run under, and how to recognize one from a
process tree all live in
[`github.com/wow-look-at-my/is-this-an-agent`](https://github.com/wow-look-at-my/is-this-an-agent),
because go-toolchain is not the only thing that needs the answer — and while it
kept its own list, the list stopped at the agents go-toolchain happened to know.

Add an agent THERE and every consumer gets it, this guard included. What lives
here is go-toolchain's own half: classifying where stdout went, and refusing to
run when the answer means the agent will never read the output.

`detectAgent` (claudeguard.go) is the whole adapter: `agent.Detect()` for the
agent, `.Name` for the abort message. Detection checks process ancestry first
and environment markers second; a marker of `0` or empty does not count. The
library's own docs carry the reasoning.

Adding an agent is one row in the roster (`agent.go` in is-this-an-agent, not
in this repo); nothing here in go-toolchain is agent-specific.

## Stdout classification

Two independent real classifiers implement `inspectStdout`/`inspectFD`, one per
platform that can actually introspect a file descriptor. A third platform
(windows) has neither and stays a documented no-op.

**`claudeguard_proc.go`** (`//go:build linux || cosmo`) classifies fd 1 via
`/proc/self/fd` + `stat`:

- **Pipe** — allowed only when `isHarnessPipeReader` says the reader is the
  agent itself: an ancestor process whose name matches a roster prefix, or an
  ancestor whose pid the agent exported (`OPENCODE_PID`). This allowance is
  load-bearing, not a nicety: grok and opencode always pipe a command's stdout
  back to themselves, so without it the guard would refuse every single run
  under them. A filter in a shell pipeline is a sibling, and a `$(...)` reader
  is a shell, so neither is an agent-named ancestor. An agent whose binary is
  renamed beyond its roster prefixes and exports no pid var fails closed.
- **Regular file** — allowed only if its path is the harness capture
  (`isHarnessCapturePath`: contains `CLAUDE_CODE_SESSION_ID`, or ends `.output`
  under a `claude` path).
- **Char device** — allowed only if it is a real terminal.

**`claudeguard_darwin.go`** (`//go:build darwin`) classifies fd 1 without
`/proc`, which darwin does not have:

- **Pipe** — darwin has no cheap way to identify the reader on the far end of
  a pipe (that needs `libproc`, not implemented here), so every pipe fails
  CLOSED — unlike linux/cosmo, there is no "the agent is reading its own pipe"
  allowance. This was the actual bug this file fixes: without ANY darwin
  classifier, `inspectStdout` fell through to the `!linux && !cosmo` no-op
  stub below, so a piped run under any agent, on real macOS, was never
  refused at all.
- **Regular file** — mode bits come from `unix.Fstat` (no path needed); the
  path itself (needed for `agent.IsCapturePath`) comes from the `F_GETPATH`
  fcntl, darwin's one substitute for `/proc/self/fd`'s readlink.
- **Char device** — `isTerminal` uses the `github.com/mattn/go-isatty`
  package already vendored at `src/compat/go-isatty` (its BSD/darwin variant),
  not a hand-rolled ioctl.

## Build constraints

Released "linux" binaries are GOOS=cosmo fat-APE slot copies, so the
classifier for that release target MUST be `linux || cosmo` — the old
`claudeguard_linux.go` was compiled out of every shipped binary and the guard
never fired in production (while GOOS=linux unit tests stayed green).
`claudeguard_buildtags_test.go` pins, per platform (linux, cosmo, darwin),
that exactly one file defining `inspectFD` is selected and the no-op stub is
excluded; the smoke-linux guard step separately asserts the shipped APE
aborts. `isTerminal` for linux/cosmo is split: `claudeguard_tty_linux.go`
(x/sys/unix TCGETS) / `claudeguard_tty_cosmo.go` (stdlib `syscall.Ioctl` + a
local TCGETS const — x/sys/unix has no cosmo port; only reachable on linux
hosts). The `!linux && !cosmo && !darwin` stub (`claudeguard_other.go`,
windows and anything else) stays a no-op. A cosmo APE actually executed on a
darwin host still fails open (its classifier is gated `linux || cosmo`, which
is satisfied at BUILD time by the cosmo target, but at RUNTIME on darwin
`/proc` genuinely does not exist, so the readlink fails and `inspectFD` falls
back to `sinkVisible`) — only a native `GOOS=darwin` build gets the real
darwin classifier.

The guard is unconditional: there is deliberately no environment variable or
flag to disable it.

## Tests

- `src/cmd/claudeguard_test.go` — sink classification, the pipe-reader
  allowance as the classifier consumes it, and the abort message naming the
  agent that hid the output (looped over `agent.Roster()`, so an agent added
  upstream is covered here without an edit). Linux-only at runtime (each test
  skips itself outside `linux`, since it exercises `claudeguard_proc.go`
  specifically). The roster's own behavior — env markers, process prefixes,
  exported pid, the ancestry walk — is tested in is-this-an-agent.
- `src/cmd/claudeguard_darwin_test.go` (`//go:build darwin`) — the darwin
  classifier's own sink classification and `fdPath`'s F_GETPATH recovery.
  Only runs when built and executed ON darwin — this repo's own CI never
  builds+tests ITSELF on darwin (`build`/`host-build` are linux-only), so
  this file needs a real Mac (or darwin CI runner) to execute, not just
  cross-compile; it's a local-developer check, not a CI gate.
- `dats/cli.dats` — the shipped binary refusing a captured run under each
  agent's marker, the `version` exemption, and the build-output deletion, for
  the linux/cosmo classifier. The suite does not assert WHICH agent the
  message names: ancestry outranks the env marker, so running the suite from
  inside another agent's session would legitimately name that agent. Runs on
  linux only (see CLAUDE.md).
- `.github/dats-fixtures/smoke-linux-agent-output-guard.dats` and
  `smoke-macos-agent-output-guard.dats`, copied by their respective CI jobs
  (`.github/workflows/ci.yml`) into a throwaway module's `dats/` directory
  (each job runs `actions/checkout` just for this), alongside a copy of the
  real shipped binary (the cosmo APE / the native darwin/arm64 binary) — not
  checked in under this repo's OWN `dats/`, since a suite referencing a
  native darwin binary would also run (and fail) during this repo's own
  linux build/host-build jobs. go-toolchain's own dats phase runs the
  fixture as part of that job's normal pipeline invocation. Mirrors
  `cli.dats`' marker-matrix/version-exemption/deletion tests (smoke-linux
  also covers the CLAUDECODE sinkDiscard path the others don't), and —
  unlike `claudeguard_darwin_test.go` — actually executes on the real runner
  on every push. Scratch space in these fixtures is always `{outputs.X}`
  (dats' own writable per-test directory), never `mktemp -d`: seatbelt
  (smoke-macos) restricts writes to exactly that directory, while bwrap
  (smoke-linux) tolerates `mktemp -d` only because it privatizes the whole
  /tmp namespace — `{outputs.X}` is the one idiom documented to work
  identically on both.
