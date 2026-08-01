# Agent output guard

`src/cmd/claudeguard.go` (+ `claudeguard_proc.go` / `claudeguard_tty_linux.go` /
`claudeguard_tty_cosmo.go` / `claudeguard_other.go`) implements the **agent
output guard**: the root `PersistentPreRunE` calls
`guardAgainstAgentOutputCapture()` (after the `skipCache` early-return, so
`cacheprog`/`version`/`install`/`release` are exempt — `cacheprog` in particular
*must* keep its stdout pipe for the GOCACHEPROG protocol) and aborts with exit 1
when go-toolchain runs under an AI coding agent **and** its stdout is anything
other than the harness transcript or a terminal — i.e. any pipe, a `> file` /
`>> file` redirect, `/dev/null`, or a `$(...)` capture.

## The agent roster

`agentHarnesses` (claudeguard.go) is the single list of agents the guard knows,
each with the environment markers it exports into every child and the process
name prefixes it runs under:

| Agent | Env markers | Process prefixes | PID vars |
|---|---|---|---|
| Claude | `CLAUDECODE` | `claude` | — |
| grok build | `GROK_AGENT` | `grok`, `xai-grok-pager` | — |
| opencode | `OPENCODE` | `opencode` | `OPENCODE_PID` |

`runningUnderAgent` checks process ancestry first (`agentProcessAncestor`, the
nearest matching ancestor wins and names the agent in the abort message), then
falls back to `agentFromEnv` — which keeps the guard working where the ancestry
walk is a no-op (non-Linux hosts) or the launcher was renamed. A marker of `0`
or empty does not count.

Adding an agent is one row in `agentHarnesses`; nothing else in the guard is
agent-specific.

## Stdout classification

`inspectStdout`/`inspectFD` (`claudeguard_proc.go`, `//go:build linux || cosmo`)
classify fd 1 via `/proc/self/fd` + `stat`:

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

## Build constraints

The classifier MUST be `linux || cosmo`: released "linux" binaries are
GOOS=cosmo fat-APE slot copies, so the old `claudeguard_linux.go` was compiled
out of every shipped binary and the guard never fired in production (while
GOOS=linux unit tests stayed green) — `claudeguard_buildtags_test.go` pins the
constraints and the smoke-linux guard step asserts the shipped APE aborts.
`isTerminal` is split: `claudeguard_tty_linux.go` (x/sys/unix TCGETS) /
`claudeguard_tty_cosmo.go` (stdlib `syscall.Ioctl` + a local TCGETS const —
x/sys/unix has no cosmo port; only reachable on linux hosts). The
`!linux && !cosmo` stub (`claudeguard_other.go`, native darwin/windows) stays a
no-op, and a cosmo APE on a darwin host stays inert via the no-`/proc` fail-open
(Readlink fails → sinkVisible).

The guard is unconditional: there is deliberately no environment variable or
flag to disable it.

## Tests

- `src/cmd/claudeguard_test.go` — the roster (env markers, process prefixes,
  exported pid), sink classification, the pipe-reader rule, and the abort
  message naming the agent that hid the output.
- `dats/cli.dats` — the shipped binary refusing a captured run under each
  agent's marker, the `version` exemption, and the build-output deletion. The
  suite does not assert WHICH agent the message names: ancestry outranks the env
  marker, so running the suite from inside another agent's session would
  legitimately name that agent.
