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
- **Socket / anon-inode** — gets the exact same `isHarnessPipeReader` chance a
  pipe gets, but NOT via `pipePeerName`: the two ends of an AF_UNIX
  `socketpair()` are separate sockets with different inodes (unlike a `pipe()`,
  where both ends share one), so an fd-target string match can never find the
  other end. `socketPeerPID` uses `getsockopt(SOL_SOCKET, SO_PEERCRED)` on the
  fd instead — the kernel's own record of the connection's creator, fixed at
  `socketpair()` time, so it still resolves after that creator (opencode/Node)
  closes its own copy of the child's fd, which real child_process plumbing
  does immediately. `pipePeerName`'s inode scan is kept only as a fallback for
  a target that SO_PEERCRED can't resolve. This closed a real gap: opencode's
  bash tool wires a spawned child's stdio through a socketpair, not a bare
  pipe, so a plain, unpiped `go-toolchain` invocation was refused as "captured
  instead of printed to the terminal" — the pipe allowance existed, but
  sockets never got a mechanism that could actually resolve their peer.
- **Regular file** — allowed only if its path is the harness capture
  (`isHarnessCapturePath`: contains `CLAUDE_CODE_SESSION_ID`, or ends `.output`
  under a `claude` path).
- **Char device** — allowed only if it is a real terminal.

**`claudeguard_darwin.go`** (`//go:build darwin`) classifies fd 1 without
`/proc`, which darwin does not have:

- **FIFO (named/anonymous pipe)** — darwin has no cheap way to identify the
  reader on the far end of a FIFO (that needs `libproc`, not implemented
  here), so every FIFO fails CLOSED — unlike linux/cosmo, there is no "the
  agent is reading its own pipe" allowance. This was the actual bug this file
  fixes: without ANY darwin classifier, `inspectStdout` fell through to the
  `!linux && !cosmo` no-op stub below, so a piped run under any agent, on real
  macOS, was never refused at all.
- **UNIX-domain socket** — unlike a FIFO, `getsockopt(SOL_LOCAL,
  LOCAL_PEERPID)` gives the exact peer pid straight from the kernel, no
  `libproc` needed, so a socket DOES get the same allowance a pipe gets on
  linux (`socketPeerPID` + `agent.CommPPID`, the latter now backed by
  `sysctl(KERN_PROC)` in is-this-an-agent's `proc_darwin.go` rather than the
  `!linux && !cosmo && !darwin` stub every darwin build used before). This
  matters because a coding agent's own tool-execution plumbing (a Node/Bun
  `child_process`'s stdio) is typically a socketpair, not a bare FIFO — the
  real bug this closed was a plain, unpiped `go-toolchain` run under
  opencode on macOS still being refused as "captured instead of printed to
  the terminal", because the socket case used to fail closed unconditionally
  with no peer check at all.
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
windows and anything else) stays a no-op.

## KNOWN GAP: the guard does not work under the APE on a darwin host

Since `matrix` defaults to one multi-platform APE, macOS ARM64 downloads that
APE rather than a native `GOOS=darwin` build — and the APE does not get the
darwin classifier. Its build tag is satisfied at BUILD time by the cosmo
target, but at RUNTIME on darwin `/proc` does not exist, the readlink fails,
and every descriptor classifies as `sinkVisible`. The guard is engaged (the
agent is still detected from its environment marker) and simply never refuses.

`unclassifiableSink` makes that state LOUD: on a non-linux host it prints an
"INOPERATIVE" banner, once per run, to the guard's own stderr writer. A guard
that silently is not running is worse than one that is loudly absent. The
banner is a notification, not a fix.

### Two states that must not collapse into one

| | meaning | today | once the host has a classifier |
|---|---|---|---|
| `unreadableDescriptorSink` | looked and saw nothing — /proc is there, this one fd would not read | allow, silently | unchanged |
| `blindClassifierSink` | blind and knows it — no mechanism on this host at all | allow, with the banner | **refuse** |

Both allow today, and that is deliberate: a classifier with no mechanism has
not earned a refusal, because it cannot tell a captured run from a legitimate
one and refusing would break every agent-driven run on that host. Once the
primitives exist, a descriptor they cannot answer for is no longer "no
mechanism" but a *failed probe* — and the honest reply is to refuse, the same
fail-closed rule `claudeguard_darwin.go` already applies to a FIFO whose reader
it cannot resolve. Keeping the two apart in the code is what makes that change
a one-line edit in the right place instead of a rewrite.

Note the difference matters for a THIRD reason, and it is not hypothetical.
`hostos.GOOS()` decides which branch is taken, and on a sandboxed Mac it
currently answers `"linux"`: `syscall.Uname` is ENOSYS on darwin under the fork
(the dispatcher has no SYS_UNAME case), and the two filesystem probes are reads
a sandbox denies, leaving the documented `"linux"` default. That routes a Mac
into the "looked and saw nothing" branch and loses even the banner.

So the darwin dispatch must NOT be built on `hostos.GOOS()` as it stands. The
fix is upstream and approved — `runtime.CosmoHostOS()`, backed by the runtime's
own `__hostos`, which rt0 sets from the APE boot path and every syscall
dispatches on, so it cannot be sandboxed away and cannot ENOSYS. `hostos` has
the seam ready (`hostSignalFunc`); wiring it is a one-line change. Both smoke
jobs assert `version host` inside dats' sandbox and outside precisely so this
stays visible until then.

Closing the gap needs three things, and one of them is not in this repo:

1. **`wow-look-at-my/is-this-an-agent` (BLOCKER).** Its `proc.go` is
   `linux || cosmo` and its sysctl-backed `proc_darwin.go` is `darwin`, so a
   cosmo APE on a mac has no process lookup at all. Without it `agent.CommPPID`
   cannot answer, and the SOCKET case — a coding agent's real stdio plumbing —
   cannot tell "the agent is reading me" (allow) from "something else is
   capturing" (refuse). Guessing either way is unacceptable: one leaves the
   guard off, the other refuses every legitimate agent run on a mac. The fix is
   a host-OS dispatch onto the sysctl path that repo already has.
2. **Here.** `inspectFD` dispatches on `hostos.GOOS()` and runs the darwin
   algorithm on a mac, SHARING `claudeguard_darwin.go`'s logic rather than
   copying it. The cosmo build must supply four primitives `x/sys/unix` gives
   only on `GOOS=darwin`: fstat mode bits, `getsockopt(SOL_LOCAL,
   LOCAL_PEERPID)`, a TIOCGETA terminal test, and `fcntl(F_GETPATH)`.
3. **`wow-look-at-my/gosmopolitan`.** Whether those four survive the fork's
   darwin syscall dispatcher is unverified. Two specific risks: `SOL_LOCAL` is
   level 0, which is `IPPROTO_IP` on Linux and may be mangled by a translating
   dispatcher; and `F_GETPATH` is a VARIADIC fcntl, which that fork documents
   (2026-07-26) must pass variadic arguments on the stack on arm64-apple or it
   reads uninitialized memory — the same defect that silently broke FD_CLOEXEC
   there.

`smoke-macos` is the gate that found this and is the gate that will prove the
fix: it runs the full pipeline under the real published APE on macos-latest.

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
  classifier's own sink classification, `fdPath`'s F_GETPATH recovery,
  `socketPeerPID`'s raw getsockopt call against a real socketpair, and an
  end-to-end subprocess test that reproduces opencode's actual plumbing (a
  socketpair standing in for stdio, `OPENCODE_PID` naming the reader) proving
  a recognized reader is let through while an unrecognized one still isn't.
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
