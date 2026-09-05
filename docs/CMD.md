# The `src/cmd` package

CLI commands (root, matrix, bench, lint, install, version, release, ignore/unignore, cacheprog) using Cobra, plus the phases they drive.

## Dependency graph submission

`dependabot.go` submits a dependency-graph snapshot to GitHub in CI. **A failed snapshot or submission is fatal to the build.**

There is deliberately NO opt-out env var. Submission is part of building in CI, and a knob that turned it off will eventually be set and left set.

Nor is "build somewhere else" a way out. `insideWorkspace()` checks the working directory against `GITHUB_WORKSPACE`, and a build outside it is a hard ERROR for every repository except `selfRepository` (`wow-look-at-my/go-toolchain`), whose smoke jobs. That one carve-out is pinned to an exact repository name precisely so it cannot become a general opt-out. `TestMaybeSubmitDeps_OtherRepoCannotSkipByBuildingElsewhere` pins the refusal.

## No self-update, but a passive update check

The binary does not self-update: it is installed and updated from buildhost (the GitHub Action downloads it with `curl`. End users use a package manager such as Homebrew/npm/APT).

It does run a passive **background update check** (`updatecheck.go`). `main.go`'s `StartUpdateCheck` starts a goroutine on every invocation except `version` (which reports its own staleness) and the `cacheprog` subprocess.

`ReportUpdateCheck`, called on every exit path — after `Execute`, before the bootstrap-failure exit, and before the "Up to date, nothing to do" fast-exit. The check never blocks the build and is silent on any error. It always runs — there is no opt-out. Override the buildhost base URL (self-hosted) with `GO_TOOLCHAIN_BUILDHOST_URL`. `version` reports its own staleness instead, from GitHub's commit API. `GO_TOOLCHAIN_GITHUB_API_URL` overrides that base.

## The up-to-date fingerprint

`uptodate.go`: the root `PersistentPreRunE` exits 0 with "Up to date, nothing to do" when the stored fingerprint matches and every build output still exists. The fingerprint is a SHA-256 over the Go version, this binary's version, `outputDir`, the run's flags, the run's environment. `.go`, `go.mod`/`go.sum`, `.dats` suites and their `.golden` snapshots, `action.yml`, anything under a `testdata` directory, and every file `go list` reports for a `//go:embed` directive.

Two of those inputs are not files, and both are there because leaving them out made the skip lie:

- **The environment.** An env-gated test or benchmark switched on between two runs is a pipeline the stored fingerprint never described. Skipping it reported a green run that never executed the thing that was turned on. Which variables a project's tests read cannot be known from here. So the whole environment is folded in except `volatileEnv` — `_`, `OLDPWD`. The snapshot is taken by `captureRunEnv` at the top of `PersistentPreRunE`, ahead of both `isUpToDate` and `saveFingerprint`. The pipeline sets variables of its own as it goes (the cacheprog's socket paths carry the PID). So hashing `os.Environ()` at save time will stamp a fingerprint no later run can match, silently disabling the skip forever.
- **The flags.** `--generate` executes go:generate directives, `--cgo` changes what gets built, `--count-generated` changes what the file-length check fails on. `flagFingerprint` folds in every root flag rather than a chosen subset, so a flag added later is covered without anyone remembering to.

There is deliberately no flag that bypasses the check. A skip that fires when something real changed is a bug in the fingerprint. And the fix is to track the input it missed.

Still untracked: a file a test reads at run time that lives outside `testdata` and under no `//go:embed` directive.

## The agent output guard

`claudeguard.go` (+ `claudeguard_proc.go` / `claudeguard_tty_*.go` / `claudeguard_other.go`): the root `PersistentPreRunE` aborts with exit 1 (deleting the module's build outputs) when go-toolchain runs under an AI coding agent AND its stdout is hidden.

WHICH agents, and how to spot one, is `github.com/wow-look-at-my/is-this-an-agent`, not this repo. Add an agent there.

Unconditional, no opt-out. `cacheprog` and `version` are exempt because neither prints a build result. `install`/`release` skip only the build cache. See `docs/AGENT-OUTPUT-GUARD.md` for the roster, the stdout classifier and the `linux||cosmo` build-tag requirement.

## cacheprog installs its logger first

`runCacheProg` (`cacheprog.go`) installs the stderr-only logger (`logger.InitSubprocess`) as its FIRST action, BEFORE config parsing, because the subprocess's stdout is the GOCACHEPROG protocol channel cmd/go parses.

## GOOS=cosmo splits

Fat APE builds use the gosmopolitan fork, whose `unix` build tag matches cosmo while `golang.org/x/sys/unix` and `modernc.org/libc` have no cosmo port. Three things split:

- **The output watchdog** is mirrored via stdlib `syscall` (`watchdog_cosmo.go`. `watchdog_unix.go` is `unix && !cosmo`). Both honor `GO_TOOLCHAIN_NO_WATCHDOG=1` via `watchdogDisabled()` in `watchdog.go` — the supported off-switch that keeps the build on its real stdio.
- **The GOCACHEPROG self-exec** goes through `cacheProgCommand` (`cacheprog.go`). On cosmo+darwin hosts it writes a `#!/bin/sh` wrapper that re-execs the APE, because on ARM64 macOS the APE never self-assimilates (shell header + compiled loader), keeps its MZ magic. Every other platform keeps the bare `<exe> cacheprog` byte-identically.
- **The persistent outdated-deps cache** is behind the `depsCache` interface, implemented in `depscache_file.go` over a JSON file under the user cache dir. It carries a check result per dependency and version, which needs no query engine. And it is compiled into every binary. `close` merges onto the file before rewriting it atomically, so a go-toolchain running alongside keeps its entries. Keep the backend free of third-party packages. The APE carries a payload per platform, and a package init that fails on any of them kills that platform's binary before `main` runs (a sqlite backend did exactly that on Windows, through `modernc.org/libc`).

## The matrix cosmo target

`targets.go` + `cosmotargets.go` + `cosmobootstrap.go`. `matrix` resolves its platforms in two cases:

- **No target flags — the default.** ONE `GOOS=cosmo` fat APE built with the gosmopolitan fork (artifact `<name>`, no `.exe`), covering `--cosmo-platforms`. One file, three platforms, one published artifact.
- **`--targets`.** An exact, validated list containing `cosmo` and/or the wasm targets (`wasm/js`, `wasm/wasip1`) — nothing else. The fat APE is the command's only native output. So a native `os/arch` pair is rejected with a pointer to `--cosmo-platforms`, which is how the APE's own host coverage is chosen.

### --cosmo-platforms

The host platforms the APE must cover, exported to the fork as `GOCOSMOPLATFORMS` so it skips building and merging the payloads nothing in the set. Default `linux/amd64,darwin/arm64,windows/amd64`. `all` leaves the variable unset, which is the fork's own everything-default.

Do not read this as a size knob. Payloads are per ARCHITECTURE, and the default set spans both — darwin/arm64 boots the arm64 image, linux/amd64 and windows/amd64 the amd64. Only a single-architecture set drops a payload (-46.9%). What the default buys is one artifact instead of six.

`cosmoRuntimeStatus` is the accepted set. And it is deliberately narrower than what the fork can emit. `darwin/amd64` (Intel-mac runtime never executed on real hardware) and `windows/arm64` (amd64-only PE payload, and WoA x86-64 emulation fails to boot it) are refused with their reason. The published platform set is what tells a consumer where the binary runs, so a platform whose runtime was never proven cannot be in it.

An older fork ignores an unknown `GOCOSMO*` variable silently, which will emit a full-coverage APE while the run reported a slimmed one. `cosmoPlatformsEnvValue` (`cosmoplatforms.go`) therefore probes support first — `go env GOCOSMOPLATFORMS` with a sentinel value, which only an aware toolchain echoes back. The artifact is still correct there: a superset APE runs on every platform claimed, and for the default set it is not even larger.

The toolchain is resolved by `EnsureCosmoToolchain` (`cosmobootstrap.go`, seam `ensureCosmoToolchainFunc`), which runs BEFORE the test phase so config errors fail fast. The key is `v<N>` parsed from the dl endpoint's redirect `Location` (`probeCosmoVersion`, a redirect-stopping HEAD), falling back to a branch-keyed dir.

`GO_TOOLCHAIN_COSMO_VERSION` pins that release. buildhost reads `v` and `branch` as alternatives. So a pinned URL carries `v=<N>` and no branch, and the pin keys the cache directly instead of probing. `go-toolchain version cosmo` (`ResolveCosmoVersion`) prints the release this host will resolve, without downloading it. `--require-release` (`cosmoReleasePattern`, `^v[0-9]`) turns the branch-key fallback into a failing exit code, since that fallback means each host will then resolve its own answer. CI uses the pair: `host-build` resolves once (with `--require-release`) and hands the answer to each `build-everywhere` leg. So the three APEs `identical` compares (via `go-toolchain verify-identical`, `src/cmd/apeidentity.go`) come from one compiler even when a run spans a gosmopolitan publish.

The cosmo build runs `<goroot>/bin/go` with `GOTOOLCHAIN=local`, `GOROOT`, a prefixed `PATH`, `CGO_ENABLED=0` always (`--cgo` warns), and `GOARCH`/`GOCOSMOFAT` cleared (fat is the fork default).

## Fork-build cache isolation

`cosmonamespace.go`: every fork-toolchain job (cosmo AND wasm) also exports `GO_TOOLCHAIN_CACHE_NAMESPACE` = `forkToolchainCacheNamespace(goroot)` — 16 hex chars of a SHA-256 over the toolchain's VERSION + `bin/` + `pkg/tool/` tool binaries.

The fork stamps a constant version, which gives DIFFERENT fork builds colliding tool/action IDs. A shared cache then serves one build's objects into another's links (the 2026-07-20 SIGSEGV-APE cross-build poisoning). The job's cacheprog scopes every cache key to that namespace (see `docs/CACHE.md`). A fingerprint failure fails the matrix run, and `runBuild` refuses a fork job whose `buildJob.cacheNamespace` is empty (last-chokepoint guard). Normal targets set no namespace and keep byte-identical cache behavior.

## Publishing one APE: the buildhost manifest

Depth: `docs/BUILDHOST-MANIFEST.md` — the wire contract, and why the filename grammar cannot carry a platform set.

With no slots (the default), `apemanifest.go` writes `buildhost-artifacts.json` next to the APE, naming the file, its platform set.

The manifest is an artifact of the build, not a survivor of it: `isOutputArtifact` matches it, so `clearBuildOutputs` and `discardBuildOutputs` delete it with the binaries. `apeManifestEntries` refuses to name a file that is not on disk, or an empty platform set.

## One APE is one file, by construction

A cosmo build writes the APE and nothing else. There is no flag, no default and no code path that copies it onto per-platform names. The copier, its `--cosmo-slots` flag and the symlink/drop machinery that hid the APE's old `_cosmo_fat` name from a publish pipeline are all gone. The behavior is not a policy CI checks after the fact — a duplicate is unreachable, so there is nothing to check.

What the deleted machinery existed for is gone too. It replaced the fat name because buildhost 400-rejected `os=cosmo` in the per-platform filename grammar. The manifest above is how the APE publishes now, under its own name, as one row.

`release --build` registers the same flags.

## The host-runnable artifact

`hostRunnableArtifact` (`matrixbuild.go`) resolves what the dats phase and the local convenience symlinks point at: the native `<name>_<hostos>_<hostarch>` build when one exists, else the APE. Without the fallback a default run — one APE, no per-platform copies — will leave both with nothing to point at.

> **APEs self-assimilate on exec.** Never execute matrix artifacts in `build/`
> in place. The bench phase never execs artifacts, so the pipeline is safe.
> smoke tests use throwaway copies only.

---

*Provenance: merged from two near-duplicate `src/cmd/` bullets that had accumulated in CLAUDE.md. Unlike `docs/ACTION.md` and `docs/CI.md`, these two did. Both are above.*
