# The `src/cmd` package

CLI commands (root, matrix, bench, lint, install, version, release,
ignore/unignore, cacheprog) using Cobra, plus the phases they drive.

## matrix walks every module, as the default pipeline does

`matrix.go`'s `runMatrixModules` calls `findGoModules()` and runs
`runReleaseWithRunner` once per module, from that module's directory. A
repository root with no `go.mod` is a tree of modules, not a broken repository:
before this, matrix tidied the root, found nothing, and died on `no go.mod
found` while a bare `go-toolchain` built the same tree fine. The action runs
`matrix`, so that gap reached every consumer that split itself into modules.
`release --build` runs the same walk.

Two rules keep a tree of modules honest:

- A module with no main package under the target's build context builds
  nothing and says so, instead of failing the run. `libraryModulesAllowed`
  (matrixrun.go) carries that permission, and only a multi-module walk sets
  it. That module's tests, vet, coverage gate and dats suites still run.
- A run that built no binary in any module fails, and names the module count.
  `matrixBuiltBinaries` counts what every module built. A single-module
  repository keeps the older message, `no main packages found to build`.

Publishing stays per working directory, so a tree whose modules each ship a
binary needs one job per module to publish them all.

## Dependency graph submission

`dependabot.go` submits a dependency-graph snapshot to GitHub in CI. **A failed
snapshot or submission is fatal to the build.**

There is deliberately NO opt-out env var: submission is part of building in CI,
and a knob that turned it off would eventually be set and left set, leaving a
repo silently absent from vulnerability scanning while its builds stayed green.

Nor is "build somewhere else" a way out. `insideWorkspace()` checks the working
directory against `GITHUB_WORKSPACE`, and a build outside it is a hard ERROR for
every repository except `selfRepository` (`wow-look-at-my/go-toolchain`), whose
smoke jobs must drive the full pipeline inside a synthetic throwaway module
under `RUNNER_TEMP`. That one carve-out is pinned to an exact repository name
precisely so it cannot become a general opt-out — the reason it exists at all is
that a snapshot describes `GITHUB_REPOSITORY` at `GITHUB_SHA`, so submitting a
fixture would publish ITS dependencies as this repository's dependency graph,
which is worse than publishing nothing.
`TestMaybeSubmitDeps_OtherRepoCannotSkipByBuildingElsewhere` pins the refusal.

## No self-update, but a passive update check

The binary does not self-update: it is installed and updated from buildhost (the
GitHub Action downloads it with `curl`; end users use a package manager such as
Homebrew/npm/APT).

It does run a passive **background update check** (`updatecheck.go`).
`main.go`'s `StartUpdateCheck` starts a goroutine on every invocation except
`version` (which reports its own staleness) and the `cacheprog` subprocess,
fetching buildhost's latest published release
(`GET {buildhostAPIBase}/api/v1/projects/go-toolchain/releases/latest`) and
comparing its `git_commit`/`published_at` against this binary's VCS stamp
(`resolvedCommit`/`resolvedTimestamp`).

`ReportUpdateCheck`, called on every exit path — after `Execute`, before the
bootstrap-failure exit, and before the "Up to date, nothing to do" fast-exit —
non-blockingly logs a one-line staleness warning (`logger.Warn`: stderr locally,
a `::warning` annotation in GitHub Actions) **if** the check already finished;
otherwise it cancels the in-flight request and moves on. The check never blocks
the build and is silent on any error. It always runs — there is no opt-out;
override the buildhost base URL (self-hosted) with `GO_TOOLCHAIN_BUILDHOST_URL`.
`version` reports its own staleness instead, from GitHub's commit API;
`GO_TOOLCHAIN_GITHUB_API_URL` overrides that base, and pointing it at an
unreachable address is how a CLI suite keeps the footer offline and instant.

## The up-to-date fingerprint

`uptodate.go`: the root `PersistentPreRunE` exits 0 with "Up to date, nothing to
do" when the stored fingerprint matches and every build output still exists. The
fingerprint is a SHA-256 over the Go version, this binary's version, `outputDir`,
the run's flags, the run's environment, and the content of every tracked file:
`.go`, `go.mod`/`go.sum`, `.dats` suites and their `.golden` snapshots,
`action.yml`, anything under a `testdata` directory, and every file `go list`
reports for a `//go:embed` directive.

Two of those inputs are not files, and both are there because leaving them out
made the skip lie:

- **The environment.** An env-gated test or benchmark switched on between two
  runs is a pipeline the stored fingerprint never described; skipping it reported
  a green run that never executed the thing that was turned on. Which variables a
  project's tests read cannot be known from here, so the whole environment is
  folded in except `volatileEnv` — `_`, `OLDPWD`, `SHLVL`, which the shell
  rewrites on every command line and nothing can read as configuration. The
  snapshot is taken by `captureRunEnv` at the top of `PersistentPreRunE`, ahead of
  both `isUpToDate` and `saveFingerprint`: the pipeline sets variables of its own
  as it goes (the cacheprog's socket paths carry the PID), so hashing
  `os.Environ()` at save time would stamp a fingerprint no later run could match,
  silently disabling the skip forever.
- **The flags.** `--generate` executes go:generate directives, `--cgo` changes
  what gets built, `--count-generated` changes what the file-length check fails
  on. `flagFingerprint` folds in every root flag rather than a chosen subset, so a
  flag added later is covered without anyone remembering to.

There is deliberately no flag that bypasses the check. A skip that fires when
something real changed is a bug in the fingerprint, and the fix is to track the
input it missed — an override would only hide the next one.

Still untracked: a file a test reads at run time that lives outside `testdata`
and under no `//go:embed` directive.

## The agent output guard

`claudeguard.go` (+ `claudeguard_proc.go` / `claudeguard_tty_*.go` /
`claudeguard_other.go`): the root `PersistentPreRunE` aborts with exit 1
(deleting the module's build outputs) when go-toolchain runs under an AI coding
agent AND its stdout is hidden — any pipe not read by the agent itself, a
`> file` redirect, `/dev/null`, or a `$(...)` capture.

WHICH agents, and how to spot one, is
`github.com/wow-look-at-my/is-this-an-agent`, not this repo; add an agent there.

Unconditional, no opt-out; `cacheprog`/`version`/`install`/`release` are exempt.
See `docs/AGENT-OUTPUT-GUARD.md` for the roster, the stdout classifier and the
`linux||cosmo` build-tag requirement.

## cacheprog installs its logger first

`runCacheProg` (`cacheprog.go`) installs the stderr-only logger
(`logger.InitSubprocess`) as its FIRST action, BEFORE config parsing, because
the subprocess's stdout is the GOCACHEPROG protocol channel cmd/go parses — a
GHA `::warning` annotation there (e.g. the deprecated
`key_id`/`access_key`/`region` config warning) would corrupt the JSON stream.

## GOOS=cosmo splits

Fat APE builds use the gosmopolitan fork, whose `unix` build tag matches cosmo
while `golang.org/x/sys/unix` and `modernc.org/libc` have no cosmo port. Three
things split:

- **The output watchdog** is mirrored via stdlib `syscall` (`watchdog_cosmo.go`;
  `watchdog_unix.go` is `unix && !cosmo`). Both honor
  `GO_TOOLCHAIN_NO_WATCHDOG=1` via `watchdogDisabled()` in `watchdog.go` — the
  supported off-switch that keeps the build on its real stdio, added as the
  bisection knob for the macOS APE pipeline wedge.
- **The GOCACHEPROG self-exec** goes through `cacheProgCommand`
  (`cacheprog.go`). On cosmo+darwin hosts it writes a `#!/bin/sh` wrapper that
  re-execs the APE, because on ARM64 macOS the APE never self-assimilates (shell
  header + compiled loader), keeps its MZ magic, and a direct fork/exec of it
  ENOEXECs — cmd/go died with
  `error starting GOCACHEPROG program ...: exec format error` on every `go`
  invocation, the visible half of the macOS wedge. Every other platform keeps
  the bare `<exe> cacheprog` byte-identically.
- **The persistent outdated-deps cache** is behind the `depsCache` interface.
  `depscache_sqlite.go` (`!cosmo`) keeps the sqlite backend and its
  `modernc.org/sqlite` blank import out of cosmo builds, while
  `depscache_cosmo.go` is a no-op cache — cosmo runs re-query the module proxy
  each time, which is cheap since up-to-date entries expired after a minute
  anyway.

## The matrix cosmo target

`targets.go` + `cosmobootstrap.go`: `matrix --targets` replaces the
`--os`×`--arch` cartesian product with an exact, validated list of `os/arch`
pairs plus the special entry `cosmo` — ONE `GOOS=cosmo` fat APE built with the
gosmopolitan fork (artifact `<name>_cosmo_fat`, no `.exe`; `--os cosmo` is
rejected with a pointer to `--targets`).

The toolchain is resolved by `EnsureCosmoToolchain` (`cosmobootstrap.go`, seam
`ensureCosmoToolchainFunc`), which runs BEFORE the test phase so config errors
fail fast: `GO_TOOLCHAIN_COSMO_GOROOT` (a local GOROOT, validated via
`bin/go version`), else a buildhost download
(`https://dl.pazer.build/gosmopolitan?branch=<GO_TOOLCHAIN_COSMO_BRANCH|master>`,
linux/amd64 hosts only) cached under `~/.cache/go-toolchain/cosmo/<key>/go`. The
key is `v<N>` parsed from the dl endpoint's redirect `Location`
(`probeCosmoVersion`, a redirect-stopping HEAD), falling back to a branch-keyed
dir.

The cosmo build runs `<goroot>/bin/go` with `GOTOOLCHAIN=local`, `GOROOT`, a
prefixed `PATH`, `CGO_ENABLED=0` always (`--cgo` warns), and
`GOARCH`/`GOCOSMOFAT` cleared (fat is the fork default).

## Fork-build cache isolation

`cosmonamespace.go`: every fork-toolchain job (cosmo AND wasm) also exports
`GO_TOOLCHAIN_CACHE_NAMESPACE` = `forkToolchainCacheNamespace(goroot)` — 16 hex
chars of a SHA-256 over the toolchain's VERSION + `bin/` + `pkg/tool/` tool
binaries.

The fork stamps a constant version, which gives DIFFERENT fork builds colliding
tool/action IDs: a shared cache then serves one build's objects into another's
links (the 2026-07-20 SIGSEGV-APE cross-build poisoning). The job's cacheprog
scopes every cache key to that namespace (see `docs/CACHE.md`); a fingerprint
failure fails the matrix run, and `runBuild` refuses a fork job whose
`buildJob.cacheNamespace` is empty (last-chokepoint guard). Normal targets set
no namespace and keep byte-identical cache behavior.

## Cosmo slots, and replacing the fat name

After the build, `copyCosmoSlots` copies the APE — real files, never symlinks,
because publish skips symlinks — onto the per-platform names in `--cosmo-slots`
(default `linux/amd64,linux/arm64,windows/amd64`; the windows slot gets `.exe`
because an APE IS a PE; `none` disables it).

`darwin/arm64`, `darwin/amd64` and `windows/arm64` are deliberate native
carve-outs: the pipeline WEDGES AT EXIT under the APE on macos-arm64 (the fork
runs unix-socket fds blocking with no netpoller on darwin hosts, so the cache
daemon's `Listener.Close` deadlocks against its own blocked `accept4` —
root-caused via SIGQUIT dumps, run 28742069477, tracked in issue #276, see
`DefaultCosmoSlots`), Intel-mac cosmo runtime is unverified, and the PE payload
is amd64-only. An explicitly-built native target wins a colliding slot filename
(the copy is skipped with a warning), and `checksums.txt` covers the copies.

**The fat name is then replaced**, because buildhost validates os on upload and
400-rejects `os=cosmo`, and one rejected artifact aborts the whole publish
(evidence: go-regex-compiler run 28738513866):

- **Locally**, `<name>_cosmo_fat` becomes a symlink to the first surviving slot
  copy. Publish skips symlinks, and matrix pre-removes a stale fat symlink
  before the next cosmo build so `go build -o` cannot write through it.
- **In CI** it is removed outright: upload-artifact DEREFERENCES symlinks, so a
  fat symlink would come back as a publish-breaking regular file inside the
  downloaded artifact.
- `checksums.txt` covers real files only.

With `--cosmo-slots=none`, or when every slot loses to a native collision, the
real fat file is kept — unpublishable to buildhost until the server accepts
`os=cosmo`. `release --build` registers the same flags.

> **APEs self-assimilate on exec.** Never execute matrix artifacts in `build/`
> in place. The bench phase never execs artifacts, so the pipeline is safe;
> smoke tests use throwaway copies only.

---

*Provenance: merged from two near-duplicate `src/cmd/` bullets that had
accumulated in CLAUDE.md. Unlike `docs/ACTION.md` and `docs/CI.md`, these two did
not contradict each other — each carried a section the other lacked (fork-build
cache isolation in one, the dependency-submission no-opt-out reasoning in the
other), and both are verified current against `src/cmd/cosmonamespace.go` and
`src/cmd/dependabot.go`. Both are above.*
