# The dats phase

After the build phase — the root pipeline at the `runBuildPhase` call site in
`root.go`, and the matrix/release path at the end of `runReleaseWithRunner`.
That also covers `release --build` — `runDatsPhase` (`src/cmd/datsphase.go`)
runs the module's [dats](https://github.com/wow-look-at-my/dats) CLI test
suites.

## dats is LINKED IN, not downloaded

go-toolchain imports `github.com/wow-look-at-my/dats` and calls
`dats.Run(ctx, dats.Options{...})` (seam: `datsRunFunc`). The suites run in
this process. There is no binary to resolve, no cache directory, no buildhost
download, no version that can drift. The old `GO_TOOLCHAIN_DATS_BIN` / `GO_TOOLCHAIN_DATS_BRANCH`
bootstrap knobs are gone with the code that needed them.

The dats library's own contract carries the interesting half. `Run` returns an
error only when the RUN could not be carried out (a bad path, a parse failure,
an unusable sandbox), while failing TESTS come back in the `Result`. So the
phase checks both — `err` and `res.Ok()` — and `Ok()` is not `Failed == 0`. A
file whose teardown command failed fails the run with every test green.

## The gate comes first

`hasDatsSuites` is a pure local filesystem walk for non-hidden `*.dats` files
under `dats/` at the module root (hidden dirs skipped; nested modules
deliberately NOT skipped — dats' own discovery doesn't skip them, and the gate
must never no-op a suite dats would run). No suites means an immediate,
silent no-op: zero output, nothing staged. The smoke jobs and every suite-less
consumer take that path.

## Repos with no go.mod

`run()` used to stop at `no go.mod found` before doing anything, which made the
dats phase unreachable for a repo that is not Go. That was the wrong boundary.
The CLI a suite exercises does not have to be written in Go, and dats is linked
in here rather than distributed on its own. The practical effect was that a
shell or TypeScript repo wanting its suites run had to fetch a standalone dats
binary and hand-wire a CI.

So when `findGoModules()` comes back empty, `run()` checks `hasDatsSuites(".")`
and, if there are suites, hands off to `runDatsOnly`:

```
⇒ No go.mod; running dats suites only
```

The suites ARE the run. There is nothing to tidy, vet, cover or build, so no
artifacts are staged and `$GO_TOOLCHAIN_DATS_BUILD_DIR` is an **empty**
directory rather than a missing.

Staging still happens under `build/`, because the sandbox exposes only the
working directory (see *Read-only in the sandbox*), but a `build/` that existed
solely for that is removed afterwards. A non-Go repo does not gitignore one and
never asked for it. A pre-existing `build/`, or one with anything else left in
it, is left alone.

Neither a module nor suites is still an error, and the message names both
halves.

The positive case is covered by unit tests (`TestRunDatsOnly*`), not by
`dats/cli.dats`. Asserting it from a suite means go-toolchain starting dats
inside a command dats is already sandboxing. The suite covers the
error message instead — and has to clear the agent markers to do.

## Staging the built binaries

`stageDatsArtifacts` copies the built binaries into `build/.dats-stage/` —
INSIDE the module root, and handed to suites as an absolute path in
`GO_TOOLCHAIN_DATS_BUILD_DIR`.

It has to be inside the module root because dats sandboxes every suite
command. A staging dir under `$TMPDIR` is
invisible to every backend, and every suite fails its setup command. `build/`
is gitignored in every repo go-toolchain builds, so staging there never
dirties the tree.

Copies, never in-place execution: the matrix cosmo artifact is a fat APE
that rewrites its own file on first exec. So nothing may ever execute a
`build/` artifact where it sits.

Staged names are the bare `OutputName` plus `.exe` on windows hosts. The root
path stages what `runBuildPhase` built; the matrix path stages the host-named
`build.BinaryName(name, hostos.GOOS(), runtime.GOARCH)` artifact. A missing
host artifact is Debug-logged and skipped, so a cross-only build still runs
its suites (and fails honestly if it needed one).

## Read-only in the sandbox

The staged binaries are READABLE inside the sandbox, not writable, and there
is no way to declare otherwise. A suite whose binary rewrites itself on first
exec (an APE does, exiting 121 from a read-only path) copies it into the
sandbox's private `/tmp` and execs the copy.

Never answer any of this by turning the sandbox off. A suite cannot even ask
for that: dats removed the file-level opt-out. So the `sandbox:` block only
NARROWS (`network`, or `image:` for something specific of the docker backend)
and disabling is `--no-sandbox` on the run. There is no toolchain-level opt-out
either — no flag and no environment variable — because one would unsandbox
every suite command in every consuming repo. This repo's own suite pins `image: golang:1.25` so the docker
backend has a Go for the bootstrap.

## The host that cannot sandbox at all

`datsSandbox` asks dats for a backend before the run and passes the answer as
`Options.Sandbox`. Auto is what almost every host gets. The exception is a host
where NO backend can exist. Bwrap is linux, seatbelt is macOS, and an NT host
is left with its own daemon.

The alternative was to fail, and failing is what takes the suites away from the
host they exist to cover. So the phase keeps every
suite and every assertion and gives up the one property it cannot have, at
error level, naming what is gone. The isolation between a suite command and the
machine. Reduced function with a signal is engineering; reduced function in
silence is the lie `claude_snippets/silent-degradation-is-a-lie.md` describes.
That is why this path is loud rather than a quiet fallback.

A missing bubblewrap on a linux host is NOT this. It carries no marker, an
install cures it, and it stays fatal — degrading there would let a fixable
setup gap turn every consuming repo's isolation.
`TestDatsSandbox` pins all three cases.

## Why the NT leg provisions no backend

CI tried to give the windows leg a linux daemon through WSL, and the attempt is
worth recording so nobody spends the afternoon again. WSL1 installs, `dockerd`
starts, and `docker info` answers — then every `docker run` dies in runc. That daemon is worse than no daemon. It passes dats' probe,
auto selects it, and every suite fails its setup command instead of taking the
`ErrNoBackendOnHost` path above. WSL2 would work and cannot be had — a
GitHub-hosted windows VM is already nested one level, and nested virtualization
cannot be enabled inside it. So `build-everywhere`'s NT leg installs nothing,
the runner's own daemon serves windows containers and is rejected by OSType.

## How the run is configured

- `Paths: []string{"dats"}` — the suite directory, nothing else.
- `Jobs: 0` (serial) on purpose: the report stays byte-deterministic, and
  staged APE copies never race their first-exec self-assimilation.
- `Sandbox` is dats' zero value, which is auto (bwrap → seatbelt → docker).
- `Env` carries only `GO_TOOLCHAIN_DATS_BUILD_DIR`. Build caching now lives
  in gosmopolitan's `cmd/go` (see [CACHE.md](CACHE.md)), which degrades
  gracefully when its shared-cache endpoint is unreachable. So a suite's own
  `go` commands need no isolation from it.
- Output goes to stdout through `logStep("Running dats suites")`, wrapped in
  `noteFirstWrite` so the step's `...` line is terminated by the report's
  first byte. Under `--json` it goes to stderr instead, so stdout stays clean
  JSON.

## Failure and coverage

A failure wraps as `dats suites failed: %w` and fails the build. On the root
path that happens before `saveFingerprint`, so a red suite is never stamped
up-to-date. `.dats` and `.golden` files feed `computeFingerprint`
(`uptodate.go`), so suite and golden edits bust the "Up to date" fast-exit.

There is deliberately NO filtering, selection, or skip mechanism at either
layer — every discovered test runs on every build (dats itself has none by
design). The repo dogfoods the phase via `dats/cli.dats`.
