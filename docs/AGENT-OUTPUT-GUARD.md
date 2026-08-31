# Agent output guard

`src/cmd/claudeguard.go` (+ `claudeguard_proc.go` / `claudeguard_darwin.go` /
`claudeguard_tty_linux.go` / `claudeguard_tty_cosmo.go` / `claudeguard_other.go`)
implements the **agent output guard**: the root `PersistentPreRunE` calls
`guardAgainstAgentOutputCapture()` for every command that prints a build
result. `cacheprog` and `version` do not, and are exempt (`skipAgentGuard`):
`cacheprog`'s stdout IS the GOCACHEPROG protocol channel, and `version` prints
four lines of build metadata -- no coverage report, no test result, nothing the
guard exists to keep in front of a reader. `version` is also what this
repository's own `dats/cli.dats` runs, and dats captures a command's
stdout to assert on it, so a guarded `version` refuses inside the dats
phase and fails every run under an agent -- the exact reader the guard is for.
`install`/`release` skip the build cache (`skipCache`) but are NOT exempt from
the guard. It aborts with exit 1
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
  ancestor whose pid the agent exported (`OPENCODE_PID`, `GROK_AGENT_PID`).
  This allowance is load-bearing, not a nicety: grok and opencode always pipe
  a command's stdout back to themselves, so without it the guard would refuse
  every single run under them. A filter in a shell pipeline is a sibling, and
  a `$(...)` reader is a shell, so neither is an agent-named ancestor. An
  agent whose binary is renamed beyond its roster prefixes and exports no pid
  var fails closed.
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
- **Char device** — allowed only if it is a real terminal AND no known
  pty-wrapping tool is an ancestor (`claudeguard_ptywrap.go`; see below).

**`claudeguard_darwin.go`** (`//go:build darwin`) classifies fd 1 without
`/proc`, which darwin does not have:

- **FIFO (named/anonymous pipe)** — grok-build captures a child's stdout
  through Rust `Stdio::piped()`, which is a FIFO on darwin, not a socketpair.
  The reader is identified by walking ancestors for the other end of this
  pipe (`proc_info(PROC_PIDFDPIPEINFO)` on a native build; `lsof` from a
  cosmo APE, the same "ask the host" pattern `CommPPID` uses for `ps`). A
  reader that is the agent itself (`harnessIsPipeReader` / `GROK_AGENT_PID`)
  is visible; `| cat` is a sibling and is not found, so it still fails
  CLOSED. An unidentified FIFO fails CLOSED too — the same rule as an
  agent renamed beyond its roster prefixes.
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
  package (its BSD/darwin variant), not a hand-rolled ioctl. A cosmo APE on a
  darwin host cannot ask that question at all: the probe reports UNSUPPORTED
  rather than "not a terminal", and going blind there would wave every
  `> /dev/null` run through, which is the shape `CLAUDECODE` takes. So an
  unaskable probe falls back to `F_GETPATH`, which DOES answer on that host,
  and the device's own path decides: a `/dev/tty…`, `/dev/pts/…`,
  `/dev/console` or `/dev/ptmx` spelling is the terminal, anything else is a
  discard. Only a descriptor whose path is unreadable too stays blind.
  `.github/dats-fixtures/agent-output-guard.dats` asserts the refusal on
  every host, so the fallback failing on darwin turns the macOS smoke leg red.

## A pty cannot name its own reader: the `script(1)` bypass

isatty answers one question -- "is this a terminal device" -- and a pty
slave answers yes to it unconditionally, whoever allocated the pty and
whatever they do with the master side. `script -qec "go-toolchain" out.log`
(util-linux) forkpty()s a FRESH pty, dup2()s it onto the child's stdin,
stdout AND stderr, and copies everything the pty produces to `out.log` --
byte for byte, the same output the guard exists to keep out of a file. Before
`claudeguard_ptywrap.go` existed, the char-device branch above stopped at
`isTerminal(fd)` returning true and classified this as `sinkVisible`: a real
terminal, nothing to refuse. It was not a real terminal a human was watching
-- it was a recording device wearing a terminal's clothes, and go-toolchain's
FULL output landed in `out.log` for an agent to read selectively instead of
in its transcript.

isatty cannot see the wrapper; ancestry can. `claudeguard_ptywrap.go` walks
this process's parent chain (`agent.CommPPID`, the same lookup agent
detection itself uses) and checks each comm against a short, exact-match
roster of tools whose whole purpose is forkpty()-ing a child to make its
isatty() checks pass: `script`, `scriptreplay`, `ttyrec`, `ttyplay`,
`asciinema`, `unbuffer`, `expect`. A char device that passes isatty AND has
one of these as an ancestor classifies as `sinkHidden` (detail: the wrapper's
name) instead of `sinkVisible`.

`tmux` and `screen` are deliberately absent from that roster: they also
forkpty(), but they relay a real pty to another real display a human
attends to, not to a file an agent can grep afterward. Adding them would
refuse every developer's ordinary multiplexed session -- the same
false-positive cost `isHarnessPipeReader`'s ancestry allowance exists to
avoid on the pipe/socket side.

This closes the same class of gap `isCapturePathFn`/`isHarnessCapturePath`
polices on the regular-file side, from the opposite direction: there, a
FILE that LOOKS hidden is let through because the harness itself owns it;
here, a TERMINAL that looks visible is refused because a known wrapper, not
a human, owns it. Both are advisory, exactly like agent detection itself
(see the package comment in `is-this-an-agent`): a renamed wrapper binary,
or one not on the roster, is a known gap, not a promise. Test coverage:
`claudeguard_ptywrap_test.go` drives the ancestry walk itself against a fake
process chain; `claudeguard_test.go`'s `TestScriptWrapperCannotFakeATerminal`
reproduces the reported bypass verbatim, for real, against the actual
`script(1)` binary; `claudeguard_darwinclassify_test.go` covers the same
branch in the build-constraint-free darwin decision table.

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
`hostos.GOOS()` decides which branch is taken, and it used to be able to answer
`"linux"` on a Mac: `syscall.Uname` is ENOSYS on darwin under the fork (the
dispatcher has no SYS_UNAME case), and the two filesystem probes are reads a
sandbox denies, leaving the `"linux"` default. That routed a Mac into the
"looked and saw nothing" branch and lost even the banner. On NT it was not a
risk but the observed behavior, since neither probe can answer there at all.

`hostos.GOOS()` no longer rests on those probes. `runtime.CosmoHostOS()` reads
the runtime's own `__hostos`, which rt0 sets from the APE boot path and every
syscall dispatches on, so it cannot be sandboxed away and cannot ENOSYS; it
lands through the `hostSignalFunc` seam ahead of everything else. The probes
stay behind it for a host the fork has no port for. Each smoke job asserts
`version host` inside dats' sandbox and outside, so an unwired seam is red.

Closing the gap took three things. One is DONE; the ordering of the other two
is the point, and it was got wrong once:

1. ~~**`wow-look-at-my/is-this-an-agent`.**~~ **MERGED.** Its `proc.go` was
   `linux || cosmo` and its sysctl-backed `proc_darwin.go` was `darwin`, so a
   cosmo APE on a mac had no process lookup; `agent.CommPPID` could not answer,
   and the SOCKET case could not tell "the agent is reading me" (allow) from
   "something else is capturing" (refuse). It now dispatches on the host and
   shells out to `ps -o ppid=,ucomm=` there, the sysctl path being uncompilable
   under cosmo.

   **It moved none of the five failing tests, and could not have.** `inspectFD`
   fails at its FIRST statement on a darwin host — the `/proc/self/fd` readlink
   — and returns before `agent.CommPPID` is called at all; that call lives
   inside the socket branch, downstream of the readlink. Necessary, not
   sufficient. The tell in CI is that "an inoperative guard announces itself"
   still passes: that banner only fires from the blind path.
2. **`wow-look-at-my/gosmopolitan` — the real gate, and it comes FIRST.**
   `F_GETPATH` and `SO_PEERCRED` for a cosmo binary on a darwin host, measured
   on Apple hardware rather than read off an allowlist. Until that is on master,
   item 3 cannot be written against anything runnable.
3. **Here, and only after 2.** `inspectFD` dispatches on `hostos.GOOS()` and
   runs the darwin algorithm on a mac, SHARING `claudeguard_darwin.go`'s logic
   rather than copying it. Write it as ORDINARY Linux-shaped syscall code —
   plain `syscall.Syscall(SYS_FCNTL, …)` and `getsockopt(SOL_SOCKET,
   SO_PEERCRED)`. The fork's dispatcher translates and already applies the
   arm64-apple variadic stack-passing fix internally, so there is no
   hand-rolled ABI work here and no `SOL_LOCAL`/`LOCAL_PEERPID` spelling: level
   0 is `IPPROTO_IP` on Linux and a blanket pass-through would silently turn an
   `IP_TTL` query into a peer-pid one.

Do not attempt item 3 against unmerged primitives. The choice there is a
temporary ref pin (forbidden) or code that cannot be run, and a guessed
syscall layer aimed at the owner's own machine is the worst place to find out.

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
  `TestScriptWrapperCannotFakeATerminal` reproduces the reported `script(1)`
  bypass for real, through the actual binary, via `runScriptWrapperHelper`.
- `src/cmd/claudeguard_ptywrap_test.go` — `ptyWrapperAncestor`'s walk against
  a fake process chain: finds a wrapper a few hops up, every roster name
  matches, a fully-resolvable wrapper-free chain answers not-found, an
  unresolvable ancestor ends the walk rather than guessing, `script` does not
  match a `scripts-runner` prefix, `tmux`/`screen` are not treated as
  recording wrappers, and a cycle terminates at `ptyWrapperMaxHops`.
  Build-constraint-free — the walk logic needs no platform primitive itself
  and runs on every OS this repo tests on.
- `src/cmd/claudeguard_darwin_test.go` (`//go:build darwin`) — the darwin
  classifier's own sink classification, `fdPath`'s F_GETPATH recovery,
  `socketPeerPID`'s raw getsockopt call against a real socketpair, and
  end-to-end subprocess tests that reproduce opencode's plumbing
  (`OPENCODE_PID`) and grok-build's (`GROK_AGENT` + `GROK_AGENT_PID`,
  socket and pipe) proving a recognized reader is let through while
  `| cat` and an unrecognized pid still refuse. Only runs when built and
  executed ON darwin — this repo's own CI never builds+tests ITSELF on
  darwin (`build`/`host-build` are linux-only), so this file needs a real
  Mac (or darwin CI runner) to execute, not just cross-compile; it's a
  local-developer check, not a CI gate.
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
- `.github/dats-fixtures/socketharness.go` spawns the binary with its stdout
  on a UNIX-domain socketpair, the stdio a Node/Bun `child_process` really
  gives a tool call, and names itself in `OPENCODE_PID` as the reader. It
  execs the binary through `/bin/sh` when a direct `execve` returns ENOEXEC.
  That fallback is not a workaround: an APE's header is valid shell rather
  than a format the kernel loads, and a POSIX shell answering ENOEXEC by
  running the file as a script is exactly what makes an APE runnable — and
  how a real agent reaches one, since a tool call is spawned through a shell.
  An APE never rewrites itself, so it stays a polyglot and the fallback is
  what reaches it. A direct exec succeeds only where binfmt_misc carries an
  `APE` entry, whose registration needs root and which macOS has no equivalent
  of, so the fallback must never be removed. Without it the harness dies with
  `exec format error` before the guard reports anything, which reads as a
  guard failure and is not one.

  It drains the socket and reprints both of the child's streams under
  `HARNESS_CHILD_STDOUT:`/`HARNESS_CHILD_STDERR:`. Reading is what an agent
  does with a tool call's stdio, and it is also the only way the child's own
  account of itself survives: `HARNESS_GUARD_REFUSED=false` is equally what a
  guard that allowed and a run that never reached the guard produce.
- **A run reaches the guard only under a module.** With no `go.mod` anywhere
  above the cwd, main.go's bootstrap cannot tell which Go to use and exits
  first, so nothing the guard would have said is ever printed. Any harness for
  these tests must therefore run inside a module; the dats fixtures get it for
  free, since `{outputs.X}` is nested inside the module go-toolchain is
  building, which is why the same empty scratch directory behaves differently
  outside dats.
