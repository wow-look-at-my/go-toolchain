# CLAUDE.md

## Build & Test

**Important:** `go build`, `go test`, etc. are often blocked in this environment because the Go toolchain version in go.mod may be newer than what's
locally installed. The go-toolchain binary handles bootstrapping the correct Go version automatically.

**Always use the released `go-toolchain` binary** to build and test. If it's not already installed, download it from buildhost. Do NOT use GitHub
Releases: that path is deprecated and frozen (CI no longer publishes there), so it serves stale binaries. CI and `action.yml` both install from
buildhost, which always has the current build:

```bash
# Download and install go-toolchain (do this first if not installed).
# Source: buildhost (pazer.build). The ?branch=v1 pin matches action.yml.
curl -fL --compressed "https://dl.pazer.build/go-toolchain?branch=v1&os=linux&arch=amd64" -o /tmp/go-toolchain
chmod +x /tmp/go-toolchain
# The linux slots serve a fat APE that self-assimilates (rewrites its own file
# to a native ELF) on first exec -- run it once while still writable, BEFORE
# installing to a root-owned location, or a non-root exec dies with
# "line 11: ... Permission denied".
/tmp/go-toolchain version
cp /tmp/go-toolchain /usr/local/bin/go-toolchain

# Build and test (runs mod tidy, vet, tests with coverage, then builds)
go-toolchain

# Cross-compile
go-toolchain matrix
```

Do NOT use `go run ./src`, `go build`, `go test`, `go vet`, or any bare `go` commands directly — they will fail if the local Go version doesn't match
go.mod.

## Coverage Analysis

After running `go-toolchain`, the output includes a "Coverage targets" section showing the top functions to test, ranked by potential gain (how much
total coverage would increase if the function were fully covered). Functions are split into two groups:

- **UNTESTED** (0% covered) — likely just needs one test that calls the function
- **PARTIAL** (some coverage) — needs specific inputs to hit uncovered branches

Each line shows: `+gain%  N stmts  file:line  FunctionName` (stmts = uncovered Go statements). Always start from the top of the list when improving
coverage.

## Project Structure

- `src/main.go` — entry point
- `src/cmd/staleoutputs.go` — **build outputs only survive a green run**. A binary at `build/<target>` is otherwise indistinguishable from one the
  current run produced, so an invocation that discards stdout+stderr and ignores the exit code can execute a previous run's binary and report a build
  that never happened. The artifacts of the module's build targets are therefore deleted: (1) `clearBuildOutputs` before any phase runs —
  `runWithRunner` (root, per module) and the top of `runReleaseWithRunner` (matrix/release), so a failure anywhere, a crash, or a kill leaves nothing
  runnable; (2) `discardBuildOutputs` on the failure path — deferred on the named error return of `run()` (registered FIRST so it runs LAST, after
  every phase has printed) and of `runReleaseWithRunner`, covering a green build followed by a red dats suite / coverage / warnings gate; (3)
  `discardBuildOutputsFromCWD` on the two exits that never enter the pipeline — the agent output guard's abort (which also NAMES the deleted paths in
  its message, so the missing binary doesn't read as a different bug) and, via the exported `DiscardBuildOutputs`, main's bootstrap-failure exit. What
  counts as an artifact is `isOutputArtifact`: the bare name, `<name>.exe`, or any `<name>_…` (BinaryName's `<name>_<goos>_<goarch>[.exe]`, the wasm
  shapes, `<name>_cosmo_fat`, the `<name>_host` symlink), minus the `nonBinaryOutputs` set (`checksums.txt`, `wasm_exec.js`, `profile.json`,
  `trace.json` — a project whose binary is named `wasm` must not lose `wasm_exec.js`). Discovery is a directory scan keyed on target NAME rather than
  a re-derivation of the platform matrix, so artifacts of a previous run's platform set go too. `clearBuildOutputs` records `{dir, names}` per module
  (`trackedOutputs`, absolute) so the failure path works from any cwd in a multi-module run. Removal failure is FATAL on the clear path (an
  undeletable binary is exactly the stale binary this prevents) and best-effort on the failure/abort paths (never mask the real error). The "Up to
  date, nothing to do" fast exit is unaffected — it fires in `PersistentPreRunE` before `run()`, and it means the last run succeeded with its outputs
  intact. No flag or env var disables any of this. NOTE for dats suites: dats runs commands in the module root, so a suite test that execs a pipeline
  command must `cd "$(mktemp -d)"` first or it deletes the binaries the pipeline just built (this bit `dats/cli.dats`'s guard test — see
  dats/README.md)
- `src/cmd/` — CLI commands (root, matrix, bench, lint, install, version, release, ignore/unignore) and every phase they drive. Depth: `docs/CMD.md`
- `src/cmd/datsphase.go` — the **dats phase**: after the build phase, `runDatsPhase` runs the module's [dats](https://github.com/wow-look-at-my/dats)
  CLI test suites. dats is LINKED IN as a library (`dats.Run`, seam `datsRunFunc`) — no download, no cached binary, no version drift. Gate first
  (`hasDatsSuites`): no `dats/` suites = silent no-op. Suites are staged into `build/.dats-stage/` (inside the module root, or the sandbox cannot see
  them) and run SANDBOXED and SERIAL; a failure fails the build. **A repo with suites but no `go.mod` runs them anyway** (`runDatsOnly`) instead of
  erroring. Never turn the sandbox off here — that is the SUITE's declaration to make. Depth: `docs/DATS-PHASE.md`
- `dats/` — this repo's own dats suite (`cli.dats` + committed `cli.snapshots/` goldens + README with the conventions): exercises the built binary's
  version/help surface, unknown-flag/-subcommand rejection (one stderr snapshot golden — regenerate with `dats --update test dats`), the
  agent-output-guard abort ("refused to run", exit 1, guard-positive via each agent's marker — `CLAUDECODE=1`, `GROK_AGENT=1`, `OPENCODE=1` — with
  dats' captured stdout — which also guarantees the bare-root test can never recurse into a nested pipeline) and the `version` exemption, and the
  update-check-silent-on-error guarantee (every exec sets `GO_TOOLCHAIN_BUILDHOST_URL=http://127.0.0.1:1` so the background check fails
  instantly+silently; the silent-check test uses `--help` because `version` never starts the background check and its staleness footer queries GitHub,
  so version tests assert only the stable `Version:`/`Commit:` lines). The guard tests assume a linux host (classifier is linux||cosmo; native
  darwin/windows are documented no-ops — same scoping as the smoke-linux guard gate, the only CI leg running this repo's pipeline on the repo).
  `.dats` + `.golden` files feed `computeFingerprint` (uptodate.go), so suite/golden edits bust the "Up to date" fast-exit
- `src/test/` — test runner, coverage parsing, watermark logic. The watermark's storage backend is platform-split: `xattr_unix.go` (`unix && !cosmo`,
  x/sys/unix xattrs; `isXattrNotFound` in the `_linux`/`_darwin` files), `xattr_windows.go` (NTFS ADS), and `xattr_cosmo.go` — GOOS=cosmo has no xattr
  wrappers in the fork's syscall package, so the attribute for target `/a/b` lives in a hidden sidecar file `/a/.b.xattr.<sanitized attr>` NEXT TO the
  target (in its parent, so the module-root watermark never dirties `git status`)
- `src/build/` — build target resolution via filesystem walking
- `src/gomod/` — shared Go module utilities (module path reading, main package discovery). `FindMainPackages` → `hasMainPackage` →
  `packageNameFromFile`
  walks the module for non-test `.go` files declaring `package main`; the package clause is read with `go/parser` in `PackageClauseOnly` mode (the old
  hand-rolled line scanner skipped only the first line of a multi-line `/* */` license header, so a k8s-style copyright block hid the package clause
  and the main package was silently not built), but **honors build constraints** first: `fileMatchesBuild` calls `go/build`'s
  `build.Default.MatchFile(dir, name)` to skip any file excluded from the build for the current context — notably the `//go:build ignore` / `// +build
  ignore` generator idiom (`//go:build ignore` + `package main`, run via `go run file.go`), plus GOOS/GOARCH filename/tag mismatches. Without this
  gate an `ignore`-tagged `package main` generator sitting next to a real `package bench`/`package e2e` would be miscounted as a main package, and
  memlimit would inject a non-ignored `package main` guard into that dir, breaking the cross-compile with `found packages bench (bench_test.go) and
  main (gomemlimit_gen.go)`. A legitimately-constrained main (e.g. a `package main` under `//go:build linux`) is still discoverable under the matching
  context — only build-excluded files are dropped. `IsNestedModule` (a non-root dir containing its own go.mod) is the shared predicate every
  filesystem walker uses to skip nested modules — `FindMainPackages`, test-package discovery (`listTestPackages` in src/test/test.go), the
  coverable-statements walk (`HasCoverableStatements` in src/test/coverable.go), build-target discovery's library-only fallback
  (`findAllPackagesByDir` in src/build), the vet fixers (gofmt, testify/gotest.tools import migrations, unused-range-vars), and the file-length check
  all skip e.g. `src/compat/go-isatty`, whose files belong to their own module and must stay byte-identical to upstream (a nested module's packages
  are not import paths of the outer module, so listing them fails `go test`/`go build` with `no required module provides package ...`)
- `src/memlimit/` — injects a stdlib-only cgroup→GOMEMLIMIT startup guard into every main package built (discovered via `gomod.FindMainPackages`,
  which
  honors build constraints so a `//go:build ignore` `package main` generator is NOT mistaken for a directory's main package) (`gomemlimit_gen.go`,
  embedded verbatim from `testdata/guard.go`), so each binary caps the Go heap at the container's cgroup memory limit instead of being OOM-killed;
  runs at the start of the build phase, gated by `GO_TOOLCHAIN_AUTO_MEMLIMIT` (on by default). The guard is a **transient** build artifact, not a
  committed file: `InjectAll` writes it just before the build and `CleanupAll` deletes it right after (wired as `defer cleanupMemLimitGuards()` in
  `runBuildPhase` and the matrix/release path in `runReleaseWithRunner`), so it never lingers in the working tree. `checkDirtyInCI` excludes
  `gomemlimit_gen.go` in every git state (added/modified/deleted, via `dirtyFilesExcludingGuard`), so the in-flight guard never counts as a dirty tree
  and a repo migrating off an older *committed* guard sheds it cleanly — `CleanupAll` deletes the committed copies and the resulting deletion is
  ignored by the check (the developer commits it once to finalize). Note the guard is deliberately **not** gitignored: the dirty-check exclusion
  handles it, and adding a `.gitignore` line would itself dirty the tree across multiple go-toolchain invocations in one CI job. That exclusion is
  invisible to the go command though, and Go 1.24+ main-module version stamping runs `git status --porcelain` while the guard exists — which used to
  stamp every built binary's `Main.Version` "+dirty" on clean checkouts (false provenance in consumer /version endpoints). So `injectMemLimitGuard`
  first calls `ensureGuardExcluded` (src/cmd/memlimitguard.go): it idempotently appends `gomemlimit_gen.go` to the repo's clone-local
  `.git/info/exclude` (resolved via `git rev-parse --git-path`, correct in linked worktrees) — under `.git/`, OUTSIDE the working tree, so unlike a
  `.gitignore` line the write cannot itself dirty anything. The entry is left in place (clone-local; also hides a stale guard from an interrupted
  build). Best-effort: no git / not a repo / write failure all silently degrade to the old `+dirty` behavior, never a failed build
- `src/cache/` — GOCACHEPROG protocol server, local + web backends, batch GET/PUT, the FUSE pack store and the stats daemon. Depth: `docs/CACHE.md`
- `src/profile/` — the **per-action build profile**: joins cmd/go's `-debug-actiongraph` dumps with the cacheprog's per-action outcome events into
  "what
  did the build spend time on, and did the cache help". `collector.go` hands out one dump path per go invocation (`Collector.GraphArg` →
  `-debug-actiongraph=<$TMPDIR/go-toolchain-profile/actiongraph-PID-SEQ.json>`; the package-level `GraphArg()` consults the `SetActive` collector so
  injection sites need no plumbing). Injection points: `src/cmd/matrixbuild.go` `runBuild` (each matrix target gets its own dump) and
  `src/test/test.go` `RunTests` — the latter via the `gotest.GraphArgFunc` hook because src/test cannot import src/profile (cycle via src/trace →
  src/summary → src/test). `graph.go` parses dumps defensively (missing file silent, malformed = one warning, never fails the build) and merges rows
  by `ActionID` — the 20-char `base64.RawURLEncoding(actionID[:15])` form, byte-identical to what `truncateActionID` emits in stat events, which is
  the join key; the executed (then longer) instance wins. `report.go` builds the `Report` (schema 1) — rows sorted by wall time
  (`TimeDone-TimeStart`), `cache_outcomes` tally, `cache_satisfied_pct`, plus `CacheTotals` and `cache.WebSummary` — and emits the console section,
  `profile.json` (written to BOTH `build/` and `$TMPDIR/go-toolchain-profile/`), and the Step Summary table. `trace.go` records executed actions into
  the Chrome trace on greedy-interval "go actions #NN" lanes (cap 32). Wiring lives in `src/cmd/profilecmd.go`: `initBuildProfile` (root run() +
  matrix runRelease; `--no-profile` opts out), `captureProfileTrace` (deferred in run() AFTER the WriteChrome defer so it runs first, stashing the
  parsed graph), and `emitBuildProfile` — called from `printCacheStats(close=true)` AFTER `cacheDaemon.Close()` and `statsListener.Close()`, so the
  web counters are post-drain-final and every per-action event has been delivered. CI gates on `build/profile.json` (poison tripwires + dead-remote
  signature — see `.github/workflows/ci.yml`)
- `src/trace/` — OpenTelemetry trace export for build pipeline timings. The OTLP/HTTP exporter construction is build-tag split: `provider_otlp.go`
  (`!cosmo`) is the real exporter; `provider_otlp_cosmo.go` is a span-dropping no-op because otlptracehttp's internal otlpconfig imports
  google.golang.org/grpc even for pure HTTP (known upstream issue, present at otel v1.44.0) and grpc cannot compile for cosmo — so **GOOS=cosmo
  binaries currently ship without OTel export** (accepted temporary regression; the full tracing API surface still works)
- `src/logger/` -- the leveled logging facility every stdout/stderr write must route through (enforced by the `bannedoutput` vet analyzer, see
  src/vet).
  Routing: Debug -> stderr; Info -> stdout; Warn/Error -> `::warning`/`::error` workflow annotations on stdout when running in GitHub Actions (GHAAuto
  checks GITHUB_ACTIONS at emit time), else stderr; Output -> stdout unconditionally (bypasses level filtering, even `silent`). Annotation message
  data and the `file=` property are escaped per the workflow-command encoding (`gha.go`: `%` -> `%25`, CR -> `%0D`, LF -> `%0A`; the property
  additionally `:` -> `%3A`, `,` -> `%2C`), so multi-line messages annotate intact instead of truncating to their first line. `InitSubprocess` is the
  stderr-only, annotation-free mode for subprocesses whose stdout is a protocol channel -- used by the cacheprog subprocess (everything, including
  Info/Output, goes to stderr; annotations stay off regardless of GITHUB_ACTIONS). The global default logger is installed by `initLogging`
  (`src/cmd/logging.go`, first thing in the root `PersistentPreRunE`) with level precedence: `--log-level` > `-v`/`--verbose` > `GOCACHE_DEBUG=1`
  (maps to debug) > info. `src/cmd/logging.go` also holds the documented held-writer bypasses (`rawStderr`/`rawStdout`) for mid-line progress
  fragments and interactive prompts the logger's auto-newline and level filtering would corrupt or hide. **Warnings budget** (`warncount.go` +
  `src/cmd/warningsgate.go`): emitted Warn/WarnFile increment `logger.WarnCount` and are retained by `EmittedWarnings`; `checkWarningsGate` fails the
  run past 15 warnings AND re-prints every counted warning as a numbered recap (one multi-line `::error` annotation in GHA). The watchdog's STALLED
  banner bypasses the logger and is NOT counted -- see docs/WARNINGS-GATE.md
- `src/vet/` — custom vet checks (assert normalization, unused imports, gotest.tools migration, banned output, testify fixes) and the auto-fixer.
  Depth:
  `docs/VET.md`
- `src/hostos/` — `hostos.GOOS()`, the host operating system as opposed to `runtime.GOOS` (what the binary was compiled for). Identical for every
  normal
  build; for a GOOS=cosmo fat APE — which reports `runtime.GOOS == "cosmo"` on Linux and macOS hosts (Windows runs the embedded native windows
  payload) — `hostos_cosmo.go` probes once: `syscall.Uname` (raw Linux syscall passes through on Linux hosts; the fork's darwin dispatcher returns
  ENOSYS, no crash), then filesystem probes (`/System/Library/CoreServices` → darwin, `/proc/self` → linux), defaulting to linux. Consumers:
  gobootstrap (go.dev archive name + `.exe` suffix), cgoenv (brew pkgconfig), codeql (platform dirs), matrix host symlinks, root/uptodate in-docker
  binary names. `runtime.GOARCH` needs no wrapper — a fat APE always runs the payload matching the host arch
- `src/compat/go-isatty/` — nested module substituted for `github.com/mattn/go-isatty` via a root go.mod `replace`: upstream selects zero
  implementation
  files under GOOS=cosmo (empty package, breaking fatih/color ← gotestsum/testjson ← src/test), so this byte-identical copy of v0.0.20 adds one
  `isatty_cosmo.go` (Fstat + S_IFCHR approximation). Non-cosmo builds compile the exact upstream files. Must be re-copied on dep bumps — see its
  README.md
- `action.yml` — the composite GitHub Action consumers use (`wow-look-at-my/go-toolchain@v1`), including the org all-builds shadow guard. Depth:
  `docs/ACTION.md`
- `.github/workflows/ci.yml` — this repo's own CI: host-build, the smoke legs, the guard gate and the release path. Depth: `docs/CI.md`

## Code Conventions

- Go module: `github.com/wow-look-at-my/go-toolchain`
- Go version: 1.24.7 (module), CI tests on 1.25
- CLI framework: `github.com/spf13/cobra`
- Test parsing: `gotest.tools/gotestsum/testjson`
- Test assertions: upstream `github.com/stretchr/testify` (`assert`/`require`) — the in-house `wow-look-at-my/testify` fork has been removed; the
  `testifycast` analyzer supplies the fork's loose cross-type numeric equality via explicit conversions
- No Makefile — use `go run ./src` as the build entry point
- Binaries are output to `build/` directory
- Platform-specific files use `_linux.go`, `_darwin.go`, `_windows.go`, `_cosmo.go` suffixes (see `src/test/xattr_*.go`). GOOS=cosmo (gosmopolitan fat
  APE) matches the `unix` build tag but NOT `linux`/`darwin` — and `golang.org/x/sys/unix` has no cosmo port, so any `//go:build unix` file that
  imports it must be constrained `unix && !cosmo` with a `_cosmo.go` counterpart using stdlib `syscall`

## Documentation

- **Always keep `README.md` up to date** when adding new features, flags, subcommands, or changing existing behavior. The README is the primary
  user-facing documentation and must accurately reflect the current state of the CLI and GitHub Action.
- When adding a new subcommand, add it to the Subcommands section and include a CLI usage example.
- When adding a new flag, add it to the appropriate flags table (persistent or command-specific).
- When changing action.yml inputs, update the Action Usage section accordingly.
- When changing the build pipeline steps (e.g. adding a new check or phase), update the "How It Works" section.
- **This file is an index; the depth lives in `docs/`.** It reached 74,061 characters — 1.85x the 40,000 an instruction file gets, all of it re-sent
  on every request of every session — so the largest entries were moved out VERBATIM, each leaving a one-line pointer: `docs/CMD.md`, `docs/CACHE.md`,
  `docs/CI.md`, `docs/ACTION.md`, `docs/VET.md`, alongside the existing `docs/DATS-PHASE.md`, `docs/AGENT-OUTPUT-GUARD.md` and
  `docs/WARNINGS-GATE.md`. Add depth to the doc, never to the bullet: an entry needing more than two or three lines wants a `docs/` file.
  Near-duplicate bullets had accumulated for `src/cmd/`, `action.yml` and `ci.yml` (two, three and three copies) — every copy was preserved in the
  extraction rather than merged, since deciding which is current needs someone who knows. Lines are hard-wrapped at 150 columns so an edit shows up as
  a reviewable diff.

## Known Issues

- **`TestStatsStreaming` (src/cache/cache_test.go)** — RESOLVED. Root cause found and fixed: a unix-socket `connect(2)` completes as soon as the
  kernel
  queues the connection, before userspace `accept(2)`, so "dial succeeded" never implied a reader existed. `StatsListener.Close` granted only a 10ms
  accept-queue grace (via a listener deadline), and once Go's poller published the expired deadline the queued connection was discarded together with
  every buffered stat event (`Puts=0` under heavy load, when the accept goroutine was starved past the window). Fixed with an accept-side ack
  handshake: the accept loop writes a 1-byte ack after registering the connection in its WaitGroup, and dialers (`NewServer`, `NewDaemon`) only keep
  the stats connection after reading that ack (5s deadline; on timeout they close the conn and run stats-off). `Close`'s `wg.Wait()` is now a real
  happens-before edge; the 10ms drain remains as belt-and-suspenders for a dialer racing Close itself.
