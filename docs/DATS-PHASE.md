# The dats phase

After the build phase — the root pipeline at the `runBuildPhase` call site in
`root.go`, and the matrix/release path at the end of `runReleaseWithRunner`,
which also covers `release --build` — `runDatsPhase` (`src/cmd/datsphase.go`)
runs the module's [dats](https://github.com/wow-look-at-my/dats) CLI test
suites.

## dats is LINKED IN, not downloaded

go-toolchain imports `github.com/wow-look-at-my/dats` and calls
`dats.Run(ctx, dats.Options{...})` (seam: `datsRunFunc`). The suites run in
this process. There is no binary to resolve, no cache directory, no buildhost
download, no version that can drift from this one, and no host that can be
missing one — the dats a build runs is the dats this binary was compiled
against. The old `GO_TOOLCHAIN_DATS_BIN` / `GO_TOOLCHAIN_DATS_BRANCH`
bootstrap knobs are gone with the code that needed them.

The dats library's own contract carries the interesting half: `Run` returns an
error only when the RUN could not be carried out (a bad path, a parse failure,
an unusable sandbox), while failing TESTS come back in the `Result`. So the
phase checks both — `err` and `res.Ok()` — and `Ok()` is not `Failed == 0`: a
file whose teardown command failed fails the run with every test green.

## The gate comes first

`hasDatsSuites` is a pure local filesystem walk for non-hidden `*.dats` files
under `dats/` at the module root (hidden dirs skipped; nested modules
deliberately NOT skipped — dats' own discovery doesn't skip them, and the gate
must never no-op a suite dats would run). No suites means an immediate,
silent no-op: zero output, nothing staged. The smoke jobs and every suite-less
consumer take that path.

## Staging the built binaries

`stageDatsArtifacts` copies the built binaries into `build/.dats-stage/` —
INSIDE the module root, and handed to suites as an absolute path in
`GO_TOOLCHAIN_DATS_BUILD_DIR`.

It has to be inside the module root because dats sandboxes every suite
command, and of the host a sandboxed command reaches only the working
directory (docker mounts it and nothing else; bwrap binds the OS tool tree
plus the cwd and overlays a private `/tmp`). A staging dir under `$TMPDIR` is
invisible to every backend, and every suite fails its setup command. `build/`
is gitignored in every repo go-toolchain builds, so staging there never
dirties the tree.

Copies, never in-place execution: matrix cosmo slot artifacts are fat APEs
that rewrite their own file on first exec, so nothing may ever execute a
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
sandbox's private `/tmp` and execs the copy — `dats/cli.dats` does exactly
that in every test.

Never answer any of this by turning the sandbox off. Whether a suite needs the
host is the SUITE's per-file declaration (`sandbox: false`, or `image:` for
something specific of the docker backend); a toolchain-level opt-out would
unsandbox every suite command in every consuming repo. This repo's own suite
pins `image: golang:1.25` so the docker backend has a Go for the bootstrap.

## How the run is configured

- `Paths: []string{"dats"}` — the suite directory, nothing else.
- `Jobs: 0` (serial) on purpose: the report stays byte-deterministic, and
  staged APE copies never race their first-exec self-assimilation.
- `Sandbox` is dats' zero value, which is auto (bwrap → seatbelt → docker).
- `Env` carries `GO_TOOLCHAIN_DATS_BUILD_DIR`, plus `GOCACHEPROG=` and
  `GOCACHE_STATS_SOCK=` — cleared, so a suite command that runs `go ...`
  cannot spawn cacheprog children of THIS binary against the outer daemon
  (stats pollution, stdout pipe stalls). Same clearing the bench runner and
  `embeddedFiles` do.
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
