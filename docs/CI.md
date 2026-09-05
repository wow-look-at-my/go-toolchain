# This repo's own CI workflow

`.github/workflows/ci.yml` — five stages: `host-build` → `build` → three `smoke-*` jobs → `publish`. It dogfoods the composite action and gates the release on the artifacts actually running.

## host-build

Builds go-toolchain from source into a host-native binary. Its cache-validation step execs `build/go-toolchain` directly, which is safe only because this job never builds the APE.

**Build-log duration regression guard.** The "Build and test" step (`go run ./src`) tees its output to `$RUNNER_TEMP/build-log.txt` (`set -euo pipefail` — GHA's default `bash -e {0}` has no `pipefail`. So a plain `cmd | tee file` will let a real build/test failure hide behind `tee`'s own exit code). This job's `cache: false` setup-go guarantees a cold module cache. So `go mod tidy -v`'s verbose `go: downloading X` / `go: found X` lines always fire — exactly the output where a real bug shipped once. A wrapper stamped even sub-second lines with a duration (`go: downloading X 0.00s`), because it forgot to gate on a minimum elapsed time (see `src/logx/logx.go`'s `minDurationToShow` and `src/cmd/console.go`'s `timedLineWriter` / `timedLineMinDuration`, both fixed to require >= 1s before stamping).

Two follow-up steps check the capture. First, `ansifilter` (not this repo's own `ansiRE`, so a bug in that regex cannot also blind the test verifying it) strips ANSI color codes — but only after a sanity check that raw ANSI codes were actually present. Then a TypeScript step asserts no `go: `-prefixed line (cmd/go's own messages, which never carry a duration themselves — any stamp there was added by us) carries a duration under 1s. So the check cannot silently pass by verifying nothing. It is deliberately scoped to `go: ` lines rather than "any duration under 1s anywhere in the log": go-toolchain's own named step/test timers (e.g. `vet: gofmt 0.17s`) are intentionally unconditional — a named operation's own time is always worth reporting — and must not be flagged.

## build

Runs the composite action (`uses: ./`) with NO target inputs and `autorelease: false`. So it exercises the exact default a consumer gets: ONE fat APE (`go-toolchain`) covering linux/amd64, darwin/arm64 and windows/amd64, plus `buildhost-artifacts.json`. The cosmo bootstrap downloads the gosmopolitan toolchain from its default `?branch=master` and cold-compiles its stdlib, hence the raised `timeout: 15`.

A trailing step asserts the shape the default exists to produce. The manifest is schema 1 with exactly one artifact, its platform set is the three above, its download filename is the plain. That last check is the one that stays honest over time — a stray `<name>_<os>_<arch>` file will silently restore the N-downloads-of-one-binary shape without failing anything else.

## build-everywhere and identical

`build` runs on ubuntu, and the three smoke jobs run THAT one binary on linux, macOS and Windows. So the smoke jobs answer "does ubuntu's APE run everywhere". That is only the same question as "does what we ship run everywhere" if every host builds the same bytes. Nothing checked that, and until `-trimpath` and `-ldflags=-buildid=` landed nothing can: the checkout path and the toolchain's own content ID both reached the build-ID notes. See [MATRIX.md](MATRIX.md) for the measurements and what each flag closes.

`build-everywhere` runs `matrix --no-benchmark` on darwin and windows and hands each result off under `ape-<origin>`. `identical` downloads those two plus `build`'s `go-build-build.broot` as the linux answer, and runs the downloaded linux APE's own `go-toolchain verify-identical` against all three. So a check that needs a Go toolchain to build never lives in the YAML itself. `fail-fast: false`, so one host failing still reports the others.

Linux comes from `build` rather than from a leg of its own. `build` already builds this repo's APE on ubuntu with the same host binary, and its hand-off is the one `publish` ships. The non-linux legs do not go through `uses: ./`: the composite action installs itself with `sudo`, which a Windows runner has not. That is why the smoke jobs stage the APE by hand too.

The NT leg builds uncached: the runner logs `GO_BUILDCACHE_CONFIG` in that step's environment and the APE then reports it unset. So the credential the job holds cannot be used there. Caching changes how long a build takes and never what it emits. So the leg still answers the question this job asks.

A missing hand-off fails rather than passing on the survivors. Comparing the hosts that answered will report green for a property no host was checked on. `publish` needs `identical`. So a build that is not reproducible never ships.

Both assertions live in `.github/dats-fixtures/`, not in the workflow. `identical.dats` asserts every host handed off an APE and that the bytes match. The jobs stage the files and invoke the suite. A workflow step schedules work and is not a test harness.

One compiler builds all three. gosmopolitan publishes on every green push. So a run that spans a publish resolved a different fork on each leg and `identical` read that as a host difference. `host-build` resolves the release once with `go-toolchain version cosmo --require-release` and exports it to each leg. See [CMD.md](CMD.md) for how the pin reaches the download URL and the cache key.

**Windows is red until the fork publishes for it.** The APE cannot complete an HTTPS request on an NT host. So it cannot download the toolchain, and buildhost serves no `gosmopolitan` windows/amd64 at master. Both are fixed by gosmopolitan's crypt32 root-store work and its windows publish leg. This job goes green when that merges. It is the same blocker smoke-windows already reports, not a new one.

Windows also failed the dirty-tree check on a line-ending difference rather than an edit. GitHub's windows image sets `core.autocrlf=true`, so the checkout wrote `go.mod` with CRLF and the Go tooling rewrote it with LF. `git status` called it modified, `git diff` normalized both sides and showed nothing, and `git update-index --refresh` settled it with `go.mod: needs update`. The repo-root `.gitattributes` pins the working tree to LF. Every tracked text blob is already LF in the index, so nothing but a Windows checkout changes.

**macOS is red on this repo's own test budget, not on anything cosmo.** The test phase runs `go test -timeout=30s` (`testTimeout`, src/test/test.go), which is a per-BINARY clock rather than a per-test one. On linux this repo already spends 25-29s of it in `src/cmd` and 19-23s. Confirming this took ruling out two other readings: the goroutine dump shows `Cmd.Wait` parked in `syscall.wait4`. So the child `go list` had not exited and both pipe copies were waiting correctly, and the last `go: downloading` line precedes the timeouts by minutes.

The 30s is deliberate and is not to be raised. The remedy is to make the two packages cheaper. What made `src/cmd` expensive was not the tests but the phase they drove. Every `runWithRunner` call ran the real vet pass, and vet loads the package graph through x/tools, which spawns a `go list` per call. The mock runner never saw those — `vet.RunWithProgress` is a direct package call — so a package that runs the pipeline dozens of times paid a subprocess. That is also what the macOS goroutine dump names: `gocommand.runCmdContext`.

`vetRunFunc` (src/cmd/testphase.go) is the seam, and `stubVetPhase` fills it in from `setupMockProject`, the same shape as `ensureCosmoToolchainFunc` keeping the build phase off buildhost. Measured on linux, `test run src/cmd` went 25.1s → 15.4s with total coverage unchanged at 84.6%: the stub still executes the call site, only the callee changes. `src/vet` is untouched by this and is now the slowest package at 19.0s.

`src/vet` cannot take the same repair — loading real packages IS what it tests. Two cheaper remedies applied instead, taking it 19.0s → 16.8s. `initGitRepo` wrote its repo-local settings straight into `.git/config` rather than spending a `git config` process per key. That is six processes per fixture repo and ten such repos. And the analyzer tests that neither chdir nor swap `os.Stderr` — `bannedoutput`, `jsoninterp`, `mapset`, `sliceset` — now call `t.Parallel`. Each analyzer's dedup state is its own package-level set. So they do not share it. `testifycast` stays serial because `applyCastFixtures` swaps `os.Stderr`.

What still blocks parallelising the REST of `src/vet` is two things. The visible one is the working directory. `os.Chdir`/`t.Chdir` appears in 9 of its test files, and a test that calls `t.Chdir` may not call `t.Parallel`. Threading a root through `vetSemantic` is what unblocks that half — `FixTestifyImports` and `MigrateGotestTools` both hardcode `WalkDir(".")`, and `packages.Load` needs. The harder one is process-wide state: the six `reset*Warnings` sets `vetSemantic` clears, and `loadDepsFromSource`. In `src/cmd` the equivalent is the `TestRunWithRunner*` family assigning the package globals `jsonOutput` and `outputDir`.

Windows is the harder case, and its numbers are measured rather than inferred. On run 33310462278 `src/vet` reported 30.166s and `src/cache` 30.353s against that 30s clock, while the same binaries take 17-20s and 9-13s on linux. The whole test phase spent 395.99s there against roughly 30s locally, at `0% cache-satisfied`. Reading that log takes care. A duration the progress printer prints against a test in a timed-out binary is an artifact whenever it reads 30.00s. Every paused `t.Parallel` test is charged the whole budget. What IS honest is a printed duration below the budget, from a test that finished, and the panic's own `running tests:` list. Take the hogs from the former and the tail from the latter. A `running tests:` list naming a single test one second in says only that the binary was past its hogs. Both hogs found so far — `src/cache`'s pack pair and `src/vet`'s canonicalize pair — came from the finished-test durations. Two attempts at intra-package parallelism failed and were reverted:

- Marking every `src/cache` test that neither chdirs nor calls `t.Setenv` broke because `setTempDir` and `setHome` mutate the process environment through a helper the sweep did. A serial test's `t.Setenv` is visible to whatever parallel tests run beside it, so the whole binary went red and then hung.
- Teaching the sweep those helpers still broke, on state that is not the environment: `TestWebBackend_PutRefusesBuildIDMismatch` goes red at once and the binary hangs after it.

A third attempt did land, on the isolated two-thirds of `src/cache`, once the classifier followed the call graph instead of the test body. It took `test run src/cache` from 11.4s to 6.51s on linux and left Windows where it was: 28.822s before, 30.337s after. That is the result to remember. The runner has four cores and `go test` already runs four package binaries side by side. So there are no spare cores for a package to parallelise INTO. The linux win came from cores Windows does not have. Concurrency is therefore not the lever here, and neither refactor below will move Windows either. What moves it is less work per binary, or a budget that knows what host it is on.

Less work per binary is what `src/cache` got. The panic on run 33312237814 named `TestPackStore_ConcurrentSameActionPutRescanConsistency` and `TestPackStore_PutAlwaysBeatsPutIfAbsent`, each eight seconds in and neither finished, against half a second apiece on linux. Both drove their racing pairs through a fresh `t.TempDir` and a fresh `OpenPackStore` per pair. So the run was mostly directory churn — cheap on tmpfs, and the thing NTFS charges most for. Every pair now races into the same store and the rescan happens at the end. That is what a real store looks like anyway. The pair count, and so the sensitivity, is unchanged. On linux each dropped to 0.10s and `test run src/cache` went 8.2s → 6.8s. Run 33313766810 confirmed it: `src/cache` passed at 23.7s, and its pack pair had spent 7.66s and 7.29s of that.

`src/vet` had the same shape. On that run `TestVetSemanticFixHoistsInitLegally` took 12.52s and `TestVetSemanticFixKeepsDocCommentQuotesAndAlignment` 7.19s, against 2.45s and 1.40s on linux, while the next slowest test in the binary was 4.41s. Each wrote its own module and ran the whole fixer over it, and a fixer run spends a `go mod tidy` and a package load. They now share one module with a file each, run the fixer once, and assert as subtests. The `go vet` that proves the rewrite compiles now covers both files. Same fixtures, same assertions, 2.05s on linux against 3.85s for the pair.

That is where the hogs run out. `src/cmd` and `src/vet` are both spread flat. The `TestRunReleaseWithRunner*` family is a dozen tests at 1-2s each on Windows against 0.2-0.3s on linux. `TestDefaultMatrixBuildsOneMultiPlatformArtifact` looks like an outlier at 5.63s against 0.23s. But it is the first test in the binary by file order and is paying.

The ratio is what decides this. And it is uniform. On one commit, with the same pipeline and the same cold cache on both legs, linux CI reported `src/vet` 8.44s, `src/cmd` 5.97s and `src/cache` 4.01s. `src/cache` is the only one of the three that finishes on Windows, at 16.6s: a factor of 4.1. Applied to the other two that puts `src/vet` near 35s and `src/cmd` at or just past the budget, which is what both do. Individual tests agree — 0.31s→1.84s, 1.71s→7.10s, 0.86s→4.32s — and raw compilation does NOT: `build runtime` took 4.41s on Windows against 4.61s on macOS. That runner is not slow at computing. It is slow at starting a process and at touching a file, which is most of what a test does here.

So the remaining gap is not a hog and not concurrency. It is a per-BINARY clock carrying a per-TEST intent. Roughly 250 sub-second tests, none of them slow, on a host uniformly four to five times slower than the one the budget was set on. Closing it means either less work per binary — more shared fixtures, each one trading away what a separate module isolates.

Must someone pick up the isolation work anyway, for its own sake: `src/cache` needs its shared state named before the rest runs in parallel. `indexCachePath` reading `os.TempDir()` is the piece already identified, and a per-backend directory on `WebConfig` will remove the environment half of the problem along.

## The smoke job

It is one matrix job over ubuntu, macOS and Windows, and every leg runs the SAME file: `.github/dats-fixtures/smoke.dats`. One APE is what every host downloads. So the question is the same everywhere, and a per-host copy of the suite is how one host's coverage quietly falls behind another's.

An answer that differs by host is asserted by PAIRING it with `uname -s` on one line — `host: windows ...|MINGW64_NT-10.0` — and matching only the combinations that agree. That keeps the assertion strong (a Linux answer on a mac still fails) without the file branching on where it runs. The same idiom carries the guard's two correct answers. A refusal on a host it can classify, the INOPERATIVE banner on one it cannot see into. The APE is copied under an `.exe` name on every host: NT needs the suffix and a posix host does not care.

The guard regression staged into the pipeline test's module is one file too. It runs INSIDE the sandbox, so its host is Linux under a docker backend and Darwin under seatbelt, and the same uname pairing covers both.

The job is `timeout-minutes`-bounded and downloads the `go-build-build.broot` hand-off the `build` job uploaded, via `wow-look-at-my/actions@cache-download#latest` (run-keyed cross-OS cache wrapper. The download `path` is the destination directory). The action names its hand-off `go-build-<job id>.b<build>` per calling job and build (the sanitized `working-directory`, `root` for `.`), with a `.m<job-index>` suffix per leg when the caller is a matrix job. So concurrent same-run saves never collide on one key. That is the only name it saves.

The suite EXECUTES throwaway copies of the artifacts in `dist/`, never the downloaded file itself. Every leg runs the SAME file, `dist/go-toolchain` — there is a single artifact now, and each leg proves it boots on that host.

**linux** — APE magic `MZqFpD`, then `version`, `--help`, host detection, and the FULL default pipeline in a tiny module under the APE. The agent-output-guard regression is a committed dats fixture (`.github/dats-fixtures/agent-output-guard.dats`), copied into that module's `dats/` dir and run automatically by the pipeline's dats phase.

**macOS** — magic, `version`, and the FULL default pipeline under the APE, plus the same guard fixture every other leg runs. This is the consumer-critical mac gate. And it is deliberately not reduced.

It used to be: darwin/arm64 shipped as a native carve-out and the mac gate ran that binary. Every pipeline PHASE went green once `cacheProgCommand` wrapped the GOCACHEPROG self-exec in a sh script. Only the exit path remained. The fork's darwin netpoller is a kqueue port now. So that deadlock must be gone. Running the full pipeline here is how we find out. A red is the honest answer that it is not, and the job's `timeout-minutes` bounds the hang.

Note which guard implementation the mac fixture now exercises. The APE reports `runtime.GOOS == "cosmo"`, so it compiles the `_cosmo` sockpeer/tty classifiers, NOT `claudeguard_darwin.go`. That file still builds for a native `go build` on a mac, but it no longer ships in any published artifact.

### smoke-macos: 5 of 10, and what the failing five are waiting on

The job is 5/10 (run 31827754447). Not worked around, and not a reason to weaken it — the gate was deliberately strengthened to run the full pipeline under the real published APE.

**The five reds are the agent output guard.** `inspectFD` classifies stdout through `/proc/self/fd`, which a darwin host does not have, so it returns at its first statement and the guard never refuses. Closing that needs gosmopolitan's `F_GETPATH`/`SO_PEERCRED` on master, and then the darwin branch of `inspectFD` written here — in that order. `docs/AGENT-OUTPUT-GUARD.md` has the chain and why the ordering is not negotiable.

Merging `is-this-an-agent`'s host dispatch moved NONE of the five, and can not have. `agent.CommPPID` is called inside the socket branch, downstream of the readlink that already failed. It was a real prerequisite for the socket cases, just not a sufficient one for any of them.

**The five greens are load-bearing, not incidental:**

- `version host` answers `host: darwin (via runtime)` inside dats' seatbelt sandbox and outside it. `runtime.CosmoHostOS()` reads the runtime's own `__hostos`, which the APE entry stub records before any Go code runs and every syscall dispatches on. So no sandbox can deny it. It landed in the fork and `hostSignalFunc` now carries it, ahead of uname and the filesystem probes. Those remain for a host the fork has no port for. Both assertions stay as regression cover, so an unwired seam fails CI instead of silently answering "linux" on a Mac.
- The INOPERATIVE banner fires. It is the only signal a human on that host gets while the guard is blind.
- `--help` and the two `version` exemptions prove the APE loads and dispatches on macOS at all.

**Windows** — magic, `version`, `--help`, host detection, a positive assertion that the agent output guard is blind here and SAYS. One dimension it still cannot match, a fork gap rather than a choice: the guard cannot fire, because the classifier reads /proc.

The pipeline used to stop at the cosmo bootstrap, and the step tolerated exactly two named blockers so a third failure can not hide behind. Buildhost carried no gosmopolitan windows/amd64 toolchain, and an APE can not resolve DNS on NT. Both have lifted — the download now succeeds — and the step that pinned them went red on the third mode. That was ours: the extraction check spelled `go/bin/go`, while a windows archive holds `go/bin/go.exe`. `cosmoGoBinPath` had always honored the host suffix, and only that one check bypassed it.

It used to stop at `--help`, on the grounds that gobootstrap downloaded `go<version>.<os>-<arch>.tar.gz` and go.dev serves windows archives as `.zip`. That reason is gone — the fork is the only toolchain now — but the platform whose payload had been dying in package init was still.

> **Owner-ruled smoke contract (Windows).** NO workflow-side Go provisioning —
> no `setup-go`, that bypasses the bootstrap requirement — and no help-flag
> `needsGo` carve-outs. `--help`'s bootstrap must resolve the runner image's
> EXISTING Go through the APE's OWN NT-side `exec.LookPath`. Broken
> pre-gosmopolitan#63 (unix-style `:` PATH walk with no `.exe` suffixing on NT
> hosts), fixed in fork v237+. If the image ever drops Go, the red is honest —
> escalate to the owner.

## publish

The single publish path, gated on all three smokes. It downloads the same `go-build-build.broot` hand-off into `build/`, then `wow-look-at-my/buildhost`'s buildhost-publish action publishes it via its `path` input and OIDC — no checkout. A trailing `if: always()` `wow-look-at-my/actions@cache-cleanup#latest` step, backed by the job's `actions: write`, deletes the run's `cache-xfer-*` hand-off entries and age-sweeps 12h-old leftovers.

The job's permissions are load-bearing (`ci.yml:498-503`):

```yaml
permissions:
  id-token: write
  contents: read
  actions: write            # the cache-cleanup step
  deployments: write        # the publish registers a GitHub Deployment
  artifact-metadata: write  # the publish posts an artifact storage record
```

`deployments: write` and `artifact-metadata: write` are what let buildhost-publish register the Deployment and post the storage record. Both are mandatory with no opt-out — see `docs/ACTION.md`.

## No GitHub Actions artifacts anywhere

Job hand-offs ride `wow-look-at-my/actions@cache-upload#latest` / `@cache-download#latest` — run-keyed GitHub cache entries whose exact key includes. A single-file upload (the `host-build`→`build` `host-go-toolchain` hand-off of `build/go-toolchain`) is stored raw and restored basename+exec-bit into the destination directory, where the action's `binary` input consumes it. The old debug-only `build-profiles` artifact is gone. The profile's home is the Step Summary table.

## Vet self-heals against export data it cannot read

`src/cmd/exportdataretry.go`. The type-check reads each dependency's export data — its compiled API — instead of its source. When go/types rejects that data, the report is a cascade of "redeclared in this block" and undefined symbols in a package the change never touched. It reads exactly like a source error, which is why several runs were re-run as flakes before the signature was recognized.

Two different things put it there, and neither is the source in front of you:

- A **damaged cache entry**, served by the shared GOCACHEPROG tier or by cmd/go's own on-disk cache.
- **Export data the importer cannot represent.** The importer is `golang.org/x/tools`, compiled into this binary against the `go/types` of whatever toolchain built it. The gosmopolitan fork is ahead of that toolchain, and its stdlib uses language features the older `go/types` refuses. No cache is involved: the data is correct and the reader is old.

`go.mod`'s `go 1.27` is the fix for the second one. And it is a floor rather than a preference. CI's `actions/setup-go` reads `go-version-file: go.mod`. So the directive is what decides which `go/types` gets linked into the binary that does the type-checking. Built against go1.26 it fails both ways. The import panics as above, and reading the same package's source instead only trades the panic for `method must have no type parameters` plus. Keep this directive at or above the fork's Go version.

There are **two** reports, and which one appears depends on how far the decode got before it hit the damage:

- `could not import <pkg> (invalid package name: "")` — the entry's header is unreadable. So the package has no name to report.
- `could not import <pkg> (reading <cachefile>: internal error in importing "<pkg>" (function with type parameters cannot have a receiver); please report an issue)` — the header decoded. The "please report an issue" wording makes this one read like a toolchain bug rather than a cache problem.

Both are recognized. Matching only the first left the second surfacing as a genuine compile error against untouched code (`could not import math/rand/v2`). That is not something a reader can act on.

`RunTestsWithCoverage` detects either report and retries the vet phase ONCE through `vet.RunFromSource`, which adds `packages.NeedDeps` so every dependency type-checks from its own source. That takes no export data as input. So an importer cannot be asked to read anything, and it covers both causes at once. `GOCACHEPROG` is unset for the rest of the run alongside it, which rules the shared tier out for the phases that follow. The retry costs one source type-check of the dependency graph and only runs after the fast path has already failed.

It warns each time it fires, naming the packages **and which of the two. A retry that hits the same report stops the run with a message saying so. Since that path read no export data, neither `go clean -cache` nor a stale importer explains it.

Bounded by construction: the retry is a single call on the failure path. So it can happen at most once.

## A test binary is built for the host, never for cosmo

`runner.Config.WithHostTarget` assigns `GOOS`/`GOARCH` from `hostos.GOOS()` and `runtime.GOARCH` on every `go` invocation whose output has to RUN here. The test run, the benchmark run, the compile check, and the `go list` calls that choose what those cover.

The fork's default `GOOS` is cosmo, and `go test` fork/execs the binary it just built. A fat APE bootstraps through a shell header, which `execve` never reads, so the kernel rejects it and every package fails identically:

```
fork/exec /tmp/go-buildNNN/b586/trace.test: exec format error
FAIL	github.com/wow-look-at-my/go-toolchain/src/trace	0.000s
```

This is not a hole in the APE-only rule. That rule governs what the pipeline SHIPS (`docs/MATRIX.md`). A test binary is a throwaway that must execute on the machine that built it. The compiler is still the fork either way.

Known gap: the up-to-date fast exit (`src/cmd/uptodate.go`) fingerprints the file list `go list` reports. And that list is per-GOOS. Vet reads the cosmo variant while the tests read the host variant. So a file excluded from the one `go list` it runs does not bust the fingerprint. Picking a variant is not the fix — the fingerprint has to cover both.

## A native test binary asks the host for a directory the APE spells differently

The section above builds the test binaries for the host. So inside a test, `os` answers as a native Windows program, while every earlier phase of the same job was the APE answering cosmo's POSIX. Two directories differ, and each one broke a test:

- `os.UserCacheDir()`. The APE answers `%USERPROFILE%\.cache`. A native binary answers `%LocalAppData%`. Two `src/cmd` bench tests drove the whole pipeline with a mock runner without stubbing the fork seam. So the build phase resolved the toolchain for real. On a warm cache that is one `go version` exec, which is why linux never showed it. NT had no warm entry under the name the test binary asked for and downloaded the toolchain instead: `27s` of a `30s` test-binary budget. `rootmocks_test.go` now points the seam at a refusal that names `stubForkToolchain`, so a pipeline test that forgets fails in milliseconds instead of reaching buildhost.
- `os.TempDir()`. Unix reads `TMPDIR`. NT reads `TMP`, then `TEMP`. Tests that moved the web index's blob into their own `t.TempDir()` by setting `TMPDIR` moved nothing on NT. That is what made `TestLoadOrFetchIndex_WarmCache304` report an empty glob. `setTempDir` in `src/cache/main_test.go` sets all three names.

Both are the argument-list boundary below, read from the other side. There a path the APE spells crosses OUT to a native tool, here a native tool's answer crosses back IN to code the APE normally.

## A path in another program's argument list crosses out of cosmo

The APE reports `GOOS=cosmo` and answers cosmo's POSIX view of the filesystem. On an NT host the tools it drives — `go.exe`, `git` — are native binaries that know nothing of that view. Which spelling is right depends on who resolves the path:

- **cosmo resolves it.** `cmd.Dir`, and the APE's own `os` calls. Cosmo translates on the way to the OS, so the POSIX spelling is correct and needs no help.
- **The other program resolves it.** Anything inside its argument list is a string that program parses, and nothing translates it. The POSIX spelling reaches NT unchanged and fails.

`src/cmd/hostscratch.go` holds the base for the second case: `scratchBase` for a caller handing it to `os.MkdirTemp`, `argListTempDir` for one joining onto it. On NT both answer the go cache directory, which is already NT-spelled. Every other host keeps `os.TempDir()`. The four crossings found so far:

| what | who parses it | failure before the fix |
| --- | --- | --- |
| `GOCACHEPROG` (`src/cmd/cacheprog.go`) | cmd/go | `error starting GOCACHEPROG program "/d/a/.../gt-ape.exe": fork/exec: The system cannot find the path specified` |
| scratch clone (`src/cmd/depsfix.go`) | git | `fatal: cannot change to /tmp/resolve-N: No such file or directory` |
| `-coverprofile` (`src/cmd/testphase.go`) | cmd/go | `open D:\a\...\smokemod\tmp\go-toolchain-cov\coverage-N.out: The system cannot find the path specified` |
| `-debug-actiongraph` (`src/cmd/profilecmd.go`) | cmd/go | not yet observed. The same crossing as the row above |

`ntPathFromPosix` (in `cacheprog.go`) is a different repair for a different input: it rewrites a shell's `/d/a/x` spelling of a path that already names a drive. It declines `/tmp/x`, which names no drive and needs a base instead.

`codeql.Analyze` builds its SARIF path the same way and is NOT fixed. Only `codeql.Extract` is wired into the pipeline. So that path has no production caller to reach it. Give it `argListTempDir` if one ever appears.

## Tidy self-heals against cache-served module-index damage

`src/cmd/modindexretry.go`'s `runModTidy` detects cmd/go's `corrupt index` failure — a damaged or mis-keyed module-index cache entry passes every content gate the cacheprog can apply.

---

*Provenance: merged from three near-duplicate `ci.yml` bullets that had accumulated in CLAUDE.md — three generations of one bullet, not three topics. Where they disagreed, the source decided. The newest carried the `.m<job-index>` matrix suffix (kept) but had DROPPED the publish job's `deployments: write` /. The oldest predated the owner-ruled Windows smoke contract entirely.*

## Step notes moved out of ci.yml

The one-line comment limit in ci.yml pushed these out of the workflow file. Each section carries the text that used to sit above the named step.

### Provision the sandbox backend (bubblewrap)

The dats phase sandboxes every suite command (dats' default), and the backend it picks decides what those commands can reach. Without bubblewrap it falls back to docker, which runs them in a container -- no host Go for the bootstrap. Installing it is what dats' own error message tells you to do on Linux. The last line is the gate: an unusable bwrap fails the job here, with its own error, instead of degrading to the fallback unnoticed.

### GITHUB_TOKEN: ${{ github.token }}

Dependency-graph submission needs a token with contents: write (granted at the workflow level above). A bare `go run ./src` does not inherit one, so pass it explicitly. Without it, submission cannot authenticate and fails loudly -- this is what wires the feature up.

### env

build/ in THIS job is the fat APE, because the fork is the only compiler and every build emits one. Executing it here is safe because the step is a shell: the APE bootstraps through a shell header, and only a raw. The APE never rewrites its own file, so the copy onto `/usr/local/bin` needs no ordering dance.

### GITHUB_TOKEN: ${{ github.token }}

The second build re-invokes go-toolchain, which also submits the dependency snapshot. Give it the same token so it succeeds rather than warning. Same job + correlator as the first submission, so it replaces it (idempotent) rather than duplicating.

### if [ "$elapsed" -gt 90 ]. Then

Caching moved into gosmopolitan's `cmd/go` (docs/CACHE.md), so this job can no longer read a cache-satisfied percentage or the poison tripwires to tell a slow runner. On the current build graph (~3300 actions -- roughly double the 1629 this budget was first derived against) an UNCHANGED second build measures 60-70s depending on the runner. 90s keeps real headroom over that without giving up on catching a genuinely broken cache. A cold first build in this same job is ~190-200s, so 90s still fails one by more than 2x. Before raising it again, confirm the second build is actually doing nothing new (no source changed between the two builds in this job) and re-measure a few runs.

The tripwires themselves are asserted by `.github/dats-fixtures/cache-profile.dats`, run by the dats action in `host-build` the same way `identical.dats` and `smoke.dats` are run by their jobs. They were a workflow step once, which meant a push was the only way to reproduce a red. The fixture runs against any local `build/profile.json`. The second-build time limit above is still a workflow step, because asserting it means driving `go-toolchain` twice and timing.

### Cross-compile socketharness

socketharness reproduces a coding agent's own tool-execution plumbing (a socketpair for a child's stdio, not a bare pipe -- see docs/AGENT-OUTPUT-GUARD.md) so smoke-linux/smoke-macos can prove the actual reported bug against the real shipped binaries. Cross-compiled here (this job already has Go set up) rather than via `setup-go` on smoke-macos. That will put Go on that runner's PATH before the "Full pipeline" step and quietly defeat the whole point of that job. Proving go-toolchain's OWN bootstrap works on a genuinely Go-less mac.

### build

Build + test via the composite action with NO target inputs, which is exactly what a consumer gets. ONE GOOS=cosmo fat APE (go-toolchain) covering linux/amd64, darwin/arm64 and windows/amd64, plus the buildhost-artifacts.json manifest that publishes it as a single multi-platform artifact. No per-platform copies, no native cross-compiles.

Publishing is NOT done here (autorelease: false): the dedicated `publish` job below is the single publish path.

### Provision the sandbox backend (bubblewrap)

The dats phase sandboxes every suite command (dats' default), and the backend it picks decides what those commands can reach. Without bubblewrap it falls back to docker, which runs them in a container -- no host Go for the bootstrap. Installing it is what dats' own error message tells you to do on Linux. The last line is the gate: an unusable bwrap fails the job here, with its own error, instead of degrading to the fallback unnoticed.

### Download host binary

Explicit name (host-build's "Upload host binary" hand-off): the strict cache-download hard-fails a nameless pick whenever the RUN holds several hand-offs. So "only one saved at this point" only ever held on attempt 1.

### uses: ./

The go-toolchain action itself cache-uploads build/ under the per-job+build name `go-build-<job id>.b<build>` on every run (unconditional) -- here that is `go-build-build.broot`, which the identical. The job id and build identity in the name keep concurrent same-run invocations (in other repos: the linux + darwin two-job pattern, or two builds in one job) from colliding on one run-scoped key. There is no standalone upload step here.

### timeout: '15

The cosmo target additionally downloads + extracts the gosmopolitan toolchain and cold-compiles its stdlib. The default 10 minutes is too tight for a cold runner.

### smoke-linux

Cross-OS smoke of the actual release artifacts: download the build-output hand-off the `build` job uploaded (exactly what `publish` will ship) and RUN the APE on each host OS. APEs self-assimilate on first exec -- they rewrite their own header in place to the host's native format -- so every job runs a throwaway copy. These jobs have no checkout, so build/ lands in an otherwise empty workspace.

### uses: actions/checkout@v7

Only for the dats fixtures under .github/dats-fixtures/ -- this job otherwise runs entirely off the downloaded release artifact.

### Download build outputs hand-off

Explicit name on purpose: by this point the run holds SEVERAL hand-offs (host-go-toolchain and socketharness-build from host-build, go-build-build.broot from build, and one ape-<origin> per build-everywhere leg), so a nameless self-discovering download will be ambiguous here. Same for smoke-macos/smoke-windows/publish below.

### Download socketharness hand-off

socketharness reproduces a coding agent's own tool-execution plumbing (a socketpair for a child's stdio, not a bare pipe -- see docs/AGENT-OUTPUT-GUARD.md) so the guard fixture below can prove the actual reported bug against the real shipped APE. Cross-compiled in host-build (which already has Go set up) rather than via setup-go here -- see smoke-macos, where installing Go on that runner will defeat the point of that job.

### Linux smoke suite

Every assertion this job makes lives in .github/dats-fixtures/smoke-linux.dats: which host the artifact detects, and the whole pipeline over a synthetic consumer. A workflow step schedules work. The harness holds the assertions. So an engineer can run them without pushing a commit.

The run is SANDBOXED, like every other suite. The pipeline test drives go-toolchain, whose OWN dats phase sandboxes the agent-output-guard fixture it stages, so a backend is resolved inside this run's backend. That nesting is the cost of keeping the isolation, and keeping it is the point. The suites exist to prove the shipped artifact behaves under what a consumer gets. The dats action exposes no way to turn it off, and nothing here must ask for one.

### the APE detects a linux host by measurement

The mirror of the same test in smoke-macos and smoke-windows: each host pins its own answer. So all three jobs assert the one thing every host-specific choice hangs off. This one must say `host: linux`, and never GUESSED. Its sandboxed twin is in the guard fixture -- worth having both, because the probes' fallback IS "linux". So the sandboxed assertion alone can pass here for the wrong reason.

### the full pipeline runs in a tiny module on a linux host
### Configure Go proxy

host-build, build, and build-everywhere fetch `GO_BUILDCACHE_CONFIG` and `GO_PROXY_CONFIG` via the secret-server step first. So `go-toolchain` runs with the shared cache and the org proxy on every host that builds this repo, not only on the linux host-build leg. The org proxy requires auth for a sumdb lookup on a module it has never resolved before, which the smoke jobs' throwaway module always. They take the secret-server step for the cache half: see the smoke-linux entry below.

### cp "$RUNNER_TEMP/gt-ape" ./gt-under-test

Full default pipeline against the shipped APE. Bootstraps a Go toolchain if the runner's is too old, then tidy/vet/test/coverage/ build.

That guard regression is a committed dats fixture (see smoke-macos) rather than hand-rolled bash: the released binaries ARE the cosmo APE. So the guard must fire in THIS artifact -- a GOOS=linux unit test cannot prove that (the guard once shipped as a `_linux.go` no-op while unit tests stayed green). It is staged inside the module root so dats' sandbox (bwrap) can reach it, same reasoning as smoke-macos.

The fork's `validateCIShared` (`cmd/go/internal/cache/shared.go`) refuses any build whose environment carries `CI`. So this job takes the secret-server step after all -- for the cache variable alone, which is why the dats step blanks `GO_PROXY_CONFIG` beside it. A throwaway module that resolves nothing twice gains little from the shared tier. What the step buys is a build the fork will start at all.

### smoke-macos

macos-latest is arm64, and darwin/arm64 is in the APE's platform set. So the APE is what ARM64 macs download. This job therefore runs the FULL default pipeline under it -- the consumer-critical gate for mac users.

This gate is deliberately not reduced. It previously ran the full pipeline against a native darwin/arm64 carve-out because the pipeline wedged AT EXIT under the APE on macOS (issue #276). The gosmopolitan runtime ran unix-socket fds blocking with no netpoller on darwin hosts. So the cache daemon's Listener.Close deadlocked against its own Accept, blocked in raw accept4(2), which a close never wakes on XNU. The fork's darwin netpoller is a kqueue port now. So the deadlock must be gone. A red here is the honest answer that it is not, and the job's timeout bounds the hang.

### uses: actions/checkout@v7

Only for the dats fixtures under .github/dats-fixtures/ -- this job otherwise runs entirely off the downloaded release artifact.

### Download socketharness hand-off

See smoke-linux for why this is a download, not a local build: no setup-go here, deliberately.

### macOS smoke suite

Every assertion this job makes lives in .github/dats-fixtures/smoke-macos.dats: host detection, the whole pipeline over a synthetic consumer, and the two unsandboxed socket cases. A workflow step schedules work. The harness holds the assertions.

The run is sandboxed. A file may narrow its own sandbox and never turn it off, and the action offers no opt-out either. So every assertion here holds under the isolation a consumer gets -- including the guard fixture go-toolchain's own dats phase runs from inside the pipeline test.

### the APE detects a darwin host by measurement

One APE runs on several hosts, so everything host-specific it does -- toolchain archives, brew paths, and the agent output guard's entire classifier. It answers from runtime.CosmoHostOS(), which no sandbox can deny. Behind that sit the uname and filesystem probes, whose fallback is "linux". So a regression that unwires the seam answers "linux" ON A MAC and every dependent decision is silently wrong. This test asserts it unsandboxed. The guard fixture asserts the same thing from inside seatbelt. Both must say darwin.

### the full pipeline runs in a tiny module on a darwin host

Full default pipeline: macos-latest has no Go on PATH. So this is the job's first real bootstrap, then tidy/vet/test/coverage/build, then the dats phase over the guard fixture staged beside the module.

That guard regression is a committed dats fixture (.github/dats-fixtures/agent-output-guard.dats), not hand-rolled bash. Go-toolchain links dats in and runs any dats/ suite found (recursively) in the module it is building -- there is no separate suite-running step. That is exactly why this fixture is copied in rather than checked in under this repo's OWN dats/. A suite asserting darwin-host behavior will also run (and fail) during this repo's own linux build/host-build jobs, which discover every dats/ suite recursively with no filtering. That inner phase sandboxes every command to the module root, so the binary under test must live INSIDE.

### the guard allows a plain run whose socket reader is the agent itself

The same two socket cases the guard fixture runs, but outside any sandbox -- the shape a real opencode user has, since nothing sandboxes them. The two are not redundant: seatbelt is itself a variable the classifier's probes answer differently under. So a disagreement between these tests and the fixture localizes the defect to the sandbox rather than to the guard.

Each case gets a go.mod in the RUN DIRECTORY ITSELF, or the child never reaches the guard. With no go on PATH, main.go's bootstrap reads the version to fetch out of ./go.mod and exits before cobra runs when there is none. It does not walk up (MEASURED: one directory above was not enough). The guard fixture never hits this: its suites run from inside go-toolchain's own pipeline, which has a go by then. The version matches what the pipeline test already cached, so this bootstraps from disk instead of downloading a second toolchain.

Every binary is copied from the pristine handed-off artifact rather than from one an earlier test ran. An APE rewrites its own file on first exec. So a copy of one that has run is no longer the thing a mac user downloads. The guard fixture copies pristine too, which is what makes the two comparable.

### Windows smoke suite

Every assertion this job makes lives in .github/dats-fixtures/smoke-windows.dats. The job checks the repo out for that one file, downloads the build hand-off, and runs the suite. A workflow step schedules work. The harness holds the assertions. So an engineer can run them without pushing a commit.

dats arrives through buildhost-download rather than through dats' own composite action, for one reason. That action lands the binary at RUNNER_TEMP/dats, and NT dispatches on the extension, so the name it chooses cannot be started here. The download names it dats.exe instead. Fixing the action upstream retires this step.

dats' backends are bwrap, sandbox-exec and docker: NT has neither of the first two, and windows-latest's docker daemon serves windows containers. So no backend here can build one. dats marks that failure ErrNoBackendOnHost. Go-toolchain's own dats phase reads the marker and runs the suites on the host, loudly. The dats ACTION has no such handling yet. So this leg is where that gap shows up -- the fix belongs there.

### the shipped artifact carries the APE magic

An APE is simultaneously a valid PE, whose embedded payload is a native windows/amd64 build. The .exe name is given at copy time: NT dispatches on the extension. And the published artifact carries none.

### the APE prints usage under --help on an NT host

Smoke contract (owner-ruled): gt must resolve the machine's EXISTING Go (windows-latest ships an image-default Go) via its own exec.LookPath on the NT side and bootstrap. Do NOT provision Go in this job (no setup-go -- that bypasses the bootstrap requirement). Do NOT exempt commands from the bootstrap. If the image drops Go, the red is honest -- escalate to the owner.

### the APE detects a windows host by measurement

The mirror of the same step in smoke-macos. One APE runs on every host, and what it detects decides every host-specific choice it makes. So each smoke job pins its own answer: `host: windows`, and never GUESSED.

This step was red the moment it was added, which is what it is for. `runtime.GOOS` is `cosmo` on NT too -- the APE's windows payload is a cosmo build, not a native one -- so `Detect()` ran the cosmo probe chain. Every host-specific choice was then made for the wrong host: the `bin/go.exe` suffix, the buildhost slot the fork downloads from, and the guard's classifier dispatch. The cure is `runtime.CosmoHostOS()` (see the smoke-macos section above). The answer is now `host: windows (via runtime)`.

### the full pipeline runs in a tiny module on an NT host

The same whole-pipeline assertion smoke-linux and smoke-macos make: the shipped APE tidies, vets, tests and builds a synthetic consumer module, and prints "Build successful". The module is three `inputs.files` entries. So the fixture carries it instead of a heredoc in a shell step.

This consumer has no org cache credentials on purpose. Gosmopolitan's own `cmd/go` treats an unconfigured shared tier as an ordinary, silent developer-machine build rather than a warning, so nothing here needs to say so.

For a while this assertion can not be made at all, and the job asserted the reachable half instead. Both have since closed: the publish job now covers the `windows/amd64` slot, and DNS resolves from NT. So a run reaches the test phase and the guard fires as designed.

What that run then found is this repo's own bug, not a fork gap: `-coverprofile` handed the native `go.exe` cosmo's `/tmp`. See "A path in another program's argument list crosses out of cosmo".

An earlier revision asserted the full pipeline on the belief that buildhost served the fork for every os/arch. It did not. And that claim was never checked.

### the agent output guard reports itself inoperative on an NT host

The one dimension Windows cannot match. The APE is a cosmo build everywhere, so claudeguard_proc.go is the classifier on NT too -- and it reads /proc, which NT does not have. The readlink fails, `unclassifiableSink` sees a host that is not linux, and `blindClassifierSink` allows the run. That is a documented decision, not an accident, and this step asserts both halves of it. A bare pipeline run with captured stdout under CLAUDECODE=1 must NOT print "refused to run". When someone teaches `inspectStdout` to classify a Windows handle, this step goes red and asks to be turned into the refusal assertion the other two.

It must also print the INOPERATIVE banner naming `windows`. That banner is the only thing a human on this host gets while the guard is blind. It doubles as a second reading of host detection from inside the guard. The banner named `linux` here until `runtime.CosmoHostOS()` was wired, which is the same defect the Host detection step above caught.

### publish

The single publish path. Gated on the cross-OS smoke jobs above so a build whose APE cannot actually run on linux/macOS/Windows is never released. Downloads the same build-output hand-off the smoke jobs ran and publishes build/ straight to buildhost (no GHA artifact involved), authenticating with a GHA OIDC token (hence id-token: write).
