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
# The linux slots serve a fat APE. It never rewrites its own file: the shell
# header extracts a loader under $TMPDIR, so a read-only binary in a read-only
# directory runs. A SHELL has to start it, though -- execve alone cannot read
# that header, which is why `go run`/`go test` of an APE says exec format error.
cp /tmp/go-toolchain /usr/local/bin/go-toolchain

# Build and test (runs mod tidy, vet, tests with coverage, then builds)
go-toolchain

# Cross-compile
go-toolchain matrix

# Integration tests run automatically after build via dats
# To add new CLI tests, add them to dats/cli.dats
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
- `src/integration/` — dats integration test runner
- `src/cmd/staleoutputs.go` — **build outputs only survive a green run**: a leftover `build/<target>` is the last thing that can pass for a build
  that never happened, so every exit that is not a green pipeline deletes the module's own artifacts. No flag or env var disables it. Depth:
  `docs/BUILD-OUTPUTS.md` (which paths count, the three sweep sites, and the dats-suite footgun)
- `src/cmd/` — CLI commands (root, matrix, bench, lint, install, version, release, ignore/unignore) and every phase they drive. Depth: `docs/CMD.md`
- `src/cmd/targets.go`, `src/cmd/cosmotargets.go`, `src/cmd/cosmoplatforms.go` — **`matrix` builds ONE fat APE**, the org's only native
  output: no target flags means one `<name>` covering `--cosmo-platforms` (`linux/amd64,darwin/arm64,windows/amd64`, exported to the
  fork as `GOCOSMOPLATFORMS`; unverified hosts are refused, and an unaware toolchain is detected and warned about rather than silently ignoring
  the set). `--targets` accepts only `cosmo` and the wasm targets (`wasm/js`, `wasm/wasip1`); a native `os/arch` pair is rejected, and no flag
  builds a per-platform native binary at all. A cosmo build writes the APE and nothing else:
  no flag copies it onto per-platform names, so a duplicate is unreachable rather than checked for. Depth: `docs/CMD.md`
- `src/cmd/gobootstrap.go`, `src/cmd/forkbuild.go`, `src/cmd/matrixbuild.go` — **the gosmopolitan fork is the only compiler, and the APE and wasm
  are the only outputs**. `EnsureGoVersion` resolves the fork onto PATH/GOROOT with `GOTOOLCHAIN=local` (the half that stops the go command
  fetching a stock toolchain for a go.mod directive); no go.dev path remains, so a fork older than the go directive fails and names the repair.
  `checkPortableJob` enforces the rule in `runBuild`, the one place anything compiles, and every compiler- or target-selecting variable is
  assigned rather than inherited. The default build phase emits the same APE `matrix` publishes, so `build/<name>` is the APE everywhere. Depth:
  `docs/MATRIX.md`
- `src/cmd/apemanifest.go` — `build/buildhost-artifacts.json`: names the APE, its platform SET and the plain filename the download is served
  under, so buildhost publishes it as ONE artifact row with one download link instead of one row per platform. Depth: `docs/BUILDHOST-MANIFEST.md`
- `src/cmd/exportdataretry.go` — export data the type-check cannot read surfaces as a cascade of undefined symbols in an untouched package, which
  reads as a source error and gets re-run as a flake. TWO reports, by how far the decode got: `invalid package name: ""` when the header is
  unreadable, `internal error in importing` when the header survives and the type graph does not. TWO causes: a damaged cache entry, and export data
  the importer cannot represent — x/tools carries the `go/types` of the toolchain that built this binary, and the fork's stdlib is ahead of it
  (`math/rand/v2`'s generic method `N[Int]` panics `NewSignatureType`). So the retry is `vet.RunFromSource` (adds `packages.NeedDeps`), which
  type-checks every dependency from source and reads no export data at all, covering both; `GOCACHEPROG` goes off alongside it. A repeat means
  neither cause applies and says so. Sibling of `modindexretry.go` (different signature, different cure). Depth: `docs/CI.md`
- `src/cmd/depsbranchenforce.go` — the branch pin is the CANONICAL form for a `github.com/wow-look-at-my/` dependency, not a
  version pin: an org require/replace carrying a plain version gets the bare `// go-toolchain:auto-branch` appended, which
  the rewrite-then-dirty-tree-fails-CI contract enforces. That costs no lookup, since the marker names no branch. A line
  already carrying the canonical marker is left alone; a legacy one is migrated, which is the one place this asks the
  remote anything (`git ls-remote --symref`, and a remote that cannot answer keeps the name and warns). A
  require overridden by a replace is marked on the replace line instead; an INDIRECT one cannot carry a working marker at
  all, so it warns and names its two repairs rather than skipping silently. `// go-toolchain:pinned <reason>` is the
  explicit opt-out. Depth: `docs/DEPS.md`
- `src/cmd/depsfix.go`, `src/cmd/depsbranch.go`, `src/cmd/deps.go`, `src/cmd/depsreport.go` — v0.0.0 repair, branch-tracked
  deps (`// go-toolchain:auto-branch`), and the same-org auto-updater; the three never fight over the same dependency.
  The marker rides a require OR a replace line -- a fork keeps upstream's module path, so it is reached through a replace,
  and the replacement's repo and version are what get resolved. A tracked branch's HEAD is the one pipeline input that is
  not a file, so the up-to-date fast exit checks it too -- otherwise a dependency that moved is invisible on an unchanged
  tree and the pin never updates. Depth: `docs/DEPS.md`
- `src/cmd/depssiblings.go`, `src/cmd/dirtypins.go` — the recorded pseudo-version on a tracked line is a CACHE of the last
  resolution, not a contract, and the two halves of making that true. A tracked module brings the modules sharing its
  repository along at the same commit, because a multi-module repo's sibling require necessarily names a commit older than
  the one publishing it -- at a first publish, one with no such module in it (`missing go.mod at revision`). Each repository
  resolves ONCE (`repoResolver`), so two of its modules cannot land on different commits. And the
  re-resolution is excluded from the CI dirty check, so it never demands a bump commit; the exclusion covers the version
  token on a same-marker line plus the `go.sum` hashes that follow it, nothing else. Depth: `docs/DEPS.md`
- Markers (`src/cmd/depsmarker.go`, `depsmatch.go`): ONE marker is the whole vocabulary. `auto-branch` names no branch:
  it follows the dependency's branch of THIS repository's name when it has one, else the DEFAULT branch -- so two repos
  developed in tandem build against each other while the change is in flight, and the merge that deletes the branch is
  what ends the match. A matched branch is never written into `go.mod`. `auto-branch=<name>` is the deliberate
  non-default choice and is never matched against. Nothing
  declares repository membership -- `repoResolver` reads that off the repository. The legacy `branch=<name>` spelling is
  still read and migrates itself, dropping a name that merely repeats the default branch. Respelling the marker again takes
  TWO releases (read it, then write it one release later): an older binary treats an unrecognized marker as an untracked
  line and appends its own comment ABOVE the require, corrupting the block. Depth: `docs/DEPS.md`
- `src/cmd/depsbranchguard.go` — a marker naming a branch that is the head of an OPEN pull request FAILS in CI and warns
  locally. That branch dies with the merge, so it resolves right up until the change lands and never again; CI is the last
  look before the merge, and tandem development across two repos is why local is only a warning. Depth: `docs/DEPS.md`
- `src/cmd/datsphase.go` — the **dats phase**: after the build phase, `runDatsPhase` runs the module's [dats](https://github.com/wow-look-at-my/dats)
  CLI test suites. dats is LINKED IN as a library (`dats.Run`, seam `datsRunFunc`) — no download, no cached binary, no version drift. Gate first
  (`hasDatsSuites`): no `dats/` suites = silent no-op. Suites are staged into `build/.dats-stage/` (inside the module root, or the sandbox cannot see
  them) and run SANDBOXED and SERIAL; a failure fails the build. **A repo with suites but no `go.mod` runs them anyway** (`runDatsOnly`) instead of
  erroring. Never turn the sandbox off here — that is the SUITE's declaration to make. Depth: `docs/DATS-PHASE.md`
- `dats/` — this repo's own dats suite (`cli.dats` + committed `cli.snapshots/` goldens + README with the conventions): exercises the built binary's
  version/help surface, unknown-flag/-subcommand rejection (one stderr snapshot golden — regenerate with `dats --update test dats`), the
  agent-output-guard abort ("refused to run", exit 1, guard-positive via each agent's marker — `CLAUDECODE=1`, `GROK_AGENT=1`, `OPENCODE=1` — with
  dats' captured stdout — which also guarantees the bare-root test can never recurse into a nested pipeline) and that `version` is NOT exempt (only
  `cacheprog` is), and the update-check-silent-on-error guarantee (every exec sets `GO_TOOLCHAIN_BUILDHOST_URL=http://127.0.0.1:1` so the background check fails
  instantly+silently; the silent-check test uses `--help` because `version` never starts the background check and its staleness footer queries GitHub,
  so version tests assert only the stable `Version:`/`Commit:` lines), and that host detection is a MEASUREMENT rather than its linux fallback.
  These guard tests assume a linux host, because this suite only runs when this repo builds ITSELF (`build`/`host-build`, linux-only). A macOS host
  gets the sibling fixture `.github/dats-fixtures/smoke-macos-agent-output-guard.dats`, which smoke-macos (which runs `actions/checkout` for exactly
  this) copies into a throwaway module and runs against the real published APE — a suite asserting darwin-host behavior cannot live under this repo's
  own `dats/`, since every suite there runs during this repo's linux self-build too. Only windows stays a documented no-op. New tests go AFTER the
  snapshot test: its INDEX names the committed golden file, so anything inserted before it renumbers the golden.
  `.dats` + `.golden` files feed `computeFingerprint` (uptodate.go), so suite/golden edits bust the "Up to date" fast-exit
- `src/runner/runner.go` — `WithHostTarget()` assigns `GOOS`/`GOARCH` from `hostos.GOOS()` and `runtime.GOARCH` on every `go` invocation whose
  output has to RUN here: the test run, the benchmark run, the compile check, and the `go list` calls choosing what those cover. The fork defaults
  to `GOOS=cosmo` and `go test` fork/execs what it builds, which answers `exec format error` — an APE bootstraps through a shell header `execve`
  never reads. The APE-only rule governs what SHIPS; a test binary is a throwaway that must run on the machine that built it, and the compiler is
  the fork either way. Depth: `docs/CI.md`
- `src/test/` — test runner, coverage parsing, watermark logic. The watermark's storage backend is platform-split: `xattr_unix.go` (`unix && !cosmo`,
  x/sys/unix xattrs; `isXattrNotFound` in the `_linux`/`_darwin` files), `xattr_windows.go` (NTFS ADS), and `xattr_cosmo.go` — GOOS=cosmo has no xattr
  wrappers in the fork's syscall package, so the attribute for target `/a/b` lives in a hidden sidecar file `/a/.b.xattr.<sanitized attr>` NEXT TO the
  target (in its parent, so the module-root watermark never dirties `git status`)
- `src/build/` — build target resolution via filesystem walking. A binary's NAME comes from the module when its main package sits at
  or one level below the module root, and from the leaf directory when deeper -- but `nameTargets` gives the module-derived name only
  to a package that is ALONE in wanting it. Two mains one level down both derive the module's name, and the old code kept whichever it
  saw first, so a build shipped missing a binary and still reported success. A contested name falls back to each package's own
  directory; one still contested after that is a hard error, never a dropped target. tmpoutput.go holds the write-then-move regime the outputs
  follow (`runBuild` in matrixbuild.go is the one chokepoint that compiles anything): the compiler's -o is `build/.tmp-<name>`
  (`TempOutputPath`), and `CommitOutput` renames it onto `build/<name>` — plus any `<base>.…` sidecar shape the cosmo fork derives from
  the -o path, never a `<base>_…` shape, which belongs to another target's own build — only after the build succeeded, failing loudly when an
  exit-0 go wrote nothing; on failure the temp spellings are deleted instead (`DiscardOutput`).
- `src/integration/` — runs a consumer module's `tests/*.dats` after the build phase (an absent directory is a silent no-op). This repo keeps no
  `tests/` of its own: a fixture spelling `go run ./src` builds an APE the fork/exec cannot start, so this repo's CLI assertions live in
  `dats/cli.dats` against the built binary
- `src/gomod/` — shared Go module utilities. `FindMainPackages` honors build constraints, so a `//go:build ignore` generator is never mistaken for a
  directory's main package. `IsNestedModule` is the shared predicate every filesystem walker skips nested modules by — their files belong to their
  own module. Depth: `docs/GOMOD.md`
- `src/memlimit/` — injects a stdlib-only cgroup→GOMEMLIMIT startup guard into every main package built (discovered via `gomod.FindMainPackages`,
  which honors build constraints so a `//go:build ignore` `package main` generator is NOT mistaken for a directory's main package)
  (`gomemlimit_gen.go`, embedded verbatim from `testdata/guard.go`), so each binary caps the Go heap at the container's cgroup memory limit instead of
  being OOM-killed; runs at the start of the build phase, unconditionally — there is deliberately NO flag or environment variable to disable injection
  (the old `GO_TOOLCHAIN_AUTO_MEMLIMIT` kill switch was removed: a build-time knob would eventually be set and left set, silently shipping binaries
  that allocate until the kernel OOM-kills them, and the run-time `GOMEMLIMIT`/`GOMEMLIMIT=off` escape hatch the guard already honors is the layer
  that actually knows whether a deployment wants the cap). The guard is a **transient** build artifact, not a committed file: `InjectAll` writes it
  just before the build and `CleanupAll` deletes it right after (wired as `defer cleanupMemLimitGuards()` in `runBuildPhase` and the matrix/release
  path in `runReleaseWithRunner`), so it never lingers in the working tree. `checkDirtyInCI` excludes `gomemlimit_gen.go` in every git state
  (added/modified/deleted, via `dirtyFilesExcludingGuard`), so the in-flight guard never counts as a dirty tree and a repo migrating off an older
  *committed* guard sheds it cleanly — `CleanupAll` deletes the committed copies and the resulting deletion is ignored by the check (the developer
  commits it once to finalize). Note the guard is deliberately **not** gitignored: the dirty-check exclusion handles it, and adding a `.gitignore`
  line would itself dirty the tree across multiple go-toolchain invocations in one CI job. That exclusion is invisible to the go command though, and
  Go 1.24+ main-module version stamping runs `git status --porcelain` while the guard exists — which used to stamp every built binary's `Main.Version`
  "+dirty" on clean checkouts (false provenance in consumer /version endpoints). So `injectMemLimitGuard` first calls `ensureGuardExcluded`
  (src/cmd/memlimitguard.go): it idempotently appends `gomemlimit_gen.go` to the repo's clone-local `.git/info/exclude` (resolved via `git rev-parse
  --git-path`, correct in linked worktrees) — under `.git/`, OUTSIDE the working tree, so unlike a `.gitignore` line the write cannot itself dirty
  anything. The entry is left in place (clone-local; also hides a stale guard from an interrupted build). Best-effort: no git / not a repo / write
  failure all silently degrade to the old `+dirty` behavior, never a failed build
- `src/cache/` — GOCACHEPROG protocol server, local + web backends, batch GET/PUT, the FUSE pack store and the stats daemon. The local tier
  defaults to LOOSE FILES and names the tier it picked on every path: go-fuse does not compile for cosmo, so a packed default gives a `go run ./src`
  build a `packs/` store the shipped APE cannot read, and the two flavors keep disjoint caches. `GOCACHE_FUSE=1` opts into packs. Depth:
  `docs/CACHE.md`
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
  `src/cmd/warningsgate.go`): the budget counts DISTINCT messages -- byte-identical text folds into one `logger.WarnCount` with a repeat count, since
  one root cause repeats per file, per package variant and (structurally) per pipeline pass, as vet's auto-fixer re-runs the whole run and would
  otherwise double every warning. `TotalWarnCount` keeps every emission and nothing is suppressed; `checkWarningsGate` fails the run past 15 distinct
  warnings AND re-prints each with its repeat count as a numbered recap (one multi-line `::error` annotation in GHA). The watchdog's STALLED banner
  bypasses the logger and is NOT counted -- see docs/WARNINGS-GATE.md
- `src/vet/` — custom vet checks (assert normalization, unused imports, gotest.tools migration, banned output, testify fixes) and the auto-fixer.
  Depth:
  `docs/VET.md`
- `src/vet/mapset.go` — the `mapset` analyzer: a `map[K]bool` is a set when its literal writes only `true`, or when the package makes it empty and
  every use is a `true` write, delete, clear, len, key-only range or index read. Both FAIL, naming `go-containers/set`. A `v, ok :=` read, a computed
  value, or the map escaping to another function keeps it a map. A `map[K]struct{}` only WARNS (deduplicated per file:line by
  `resetMapSetWarnings`; the `set` package itself is exempt, `isSetPackage`, since `Set[T]` IS that map) — it already carries no value. No opt-out marker, and no
  module skips the check: an org module FAILS on the bool findings (`isOrgModule`), everyone else WARNS on them. A bool finding is also REWRITTEN
  wherever every use is visible — see `src/vet/setfix.go`. Depth: `docs/VET.md`
- `src/vet/sliceset.go` — the `sliceset` analyzer: told a `map[K]bool` is a set, the cheapest exit is a slice and `slices.Contains`, which answers the
  same question by walking everything ever added. So a slice the package creates and asks membership of is a set too, on the same org-FAILS /
  everyone-else-WARNS split. Three findings: a literal spelled inside the lookup, `if !slices.Contains(s, v) { s = append(s, v) }` (add-if-absent IS
  an insert), and a slice whose every use is a set op. A loop comparing each element to one value IS `slices.Contains`, so writing the scan out by
  hand does not escape. Position and repetition are what a slice has and a set does not, so an index, a keyed range, a spread, or the slice as an
  argument or a return keeps it a slice — `validGOOS`'s `strings.Join` is the honest version of that. A parameter belongs to its caller. Depth:
  `docs/VET.md`
- `src/vet/setfix.go` — the fixer both set checks share: `make`→`set.New[K]()`, an all-true or element literal→`set.Of[K](…)`, `m[k]=true` and
  `append`→`Add`, a read and `slices.Contains`→`Contains`, `delete`→`Remove`, `len`→`Len`, `range`→`range …All()`, and the import. The type argument
  is explicit, because `set.Of(1, 2)` off a `[]float64` literal infers `int`. ONE use with no set spelling blocks the whole variable — half a rewrite
  does not compile — and so does a package-level variable this pass cannot see every use of (exported, or a directory holding a `_test.go` the plain
  package variant does not carry). Depth: `docs/VET.md`
- `src/vet/writeruns.go` — the `writeruns` analyzer: three or more adjacent statements writing source-spelled text to ONE writer are a document
  nobody can read in the source, so the third and each later write WARNS and names `text/template`. Never a build failure by itself; a long run still
  fails through the warnings budget, which this repo's 25-write mermaid header did. A run ends at any other statement, at a different writer, and at
  a write whose text is computed (`b.WriteByte(c)`); a writer that digests its input never counts (`isHashWriter`). Every module, warning severity,
  no opt-out marker. Depth: `docs/VET.md`
- `src/vet/jsoninterp.go` — the `jsoninterp` analyzer: a JSON document built out of string pieces, by a `fmt` format string, by a `+` concatenation,
  or by a template. None of the three escapes for JSON, so a quote or a backslash in a value breaks the DOCUMENT and a value the user controls
  chooses the object; `%q` is Go quoting, not JSON. There is no `json/template` and no JSON context in either template package — html/template's
  `json.Marshal` escaper is reachable only inside a `<script>` — so a JSON template is reported too, which is the one place this and `writeruns`
  point in opposite directions. The shape test (`jsonshape.go`) is deliberately narrow, so prose quoting an example is silent. Org modules FAIL,
  everyone else WARNS, no opt-out marker. Depth: `docs/VET.md`
- `src/vet/commentnumbers.go` — the `commentnumbers` analyzer: a number in a comment, in digits or in words, is a count of what exists
  today, and the edit that adds an item leaves it wrong — so it is banned, and the message names the remedy (describe what the code does and
  let the reader count; cite a section of a spec by its unique slug or heading, never by its position). A digit run touching a letter is a
  name (`sha256`, `amd64`, `10ms`), as is a qualified name (`net/http`,
  `example.com/mod/v2`) and anything inside a URL, which is how a reference carrying a number survives. A section sign (`§7.3`, `§ 4`) exempts
  the number it introduces — the citation form for a document that publishes no slug — and `HTTP` immediately before a status-code-width digit
  run exempts that code, so a bare `403` is still a count. A currency sign against the digits exempts the amount (`$1.43`), which states a cost
  rather than counting anything below it; `costs $ 5` is a count again. A whole word naming a number is
  reported, so `once` and `One` go and `someone` stays. Directives and generated files are skipped. A WARNING in every module — stale prose must
  not fail a build by itself, and the warnings budget is what turns a repo full of them red. A warning is spent per file:line, so a sentence
  naming several numbers costs one. No opt-out marker. Depth: `docs/VET.md`
- `src/hostos/` — `hostos.GOOS()`, the host OS as opposed to `runtime.GOOS` (what the binary was compiled for). A fat APE reports
  `runtime.GOOS == "cosmo"` on **every** host, Windows included — there is no native windows payload to fall back on, which is how NT silently took
  the `"linux"` default. The answer comes from `runtime.CosmoHostOS()`, the runtime's own `__hostos`: the APE entry stub records it before any Go
  code runs and every syscall dispatches on it, so no sandbox can deny it and no target can ENOSYS it. It arrives through the `hostSignalFunc` seam
  ahead of `syscall.Uname` and the filesystem probes (`/System/Library/CoreServices` → darwin, `/proc/self` → linux), which stay for a host the fork
  has no port for and end in a `"linux"` GUESS. So `Detect()` returns the METHOD alongside the answer, a guess prints a one-time banner, and
  `go-toolchain version host` shows both; each smoke job asserts its own host, inside dats' sandbox and outside. Consumers: cosmobootstrap (the
  buildhost slot and the fork's `bin/go` suffix), cgoenv (brew pkgconfig), codeql (platform dirs), matrix host symlinks, and the agent output guard's
  classifier dispatch. `runtime.GOARCH` needs no wrapper — a fat APE always runs the payload matching the host arch
- `action.yml` — the composite GitHub Action consumers use (`wow-look-at-my/go-toolchain@v1`), including the org all-builds shadow guard. Depth:
  `docs/ACTION.md`
- `.github/workflows/ci.yml` — this repo's own CI: host-build, the smoke legs, the guard gate and the release path. Depth: `docs/CI.md`

## Code Conventions

- Go module: `github.com/wow-look-at-my/go-toolchain`
- Go version: 1.27 (module). It is a FLOOR set by the fork, not a preference: the pipeline type-checks code the gosmopolitan fork compiles, and
  `go/types` links in from whatever toolchain built this binary. Built with go1.26 it cannot read the fork's export data (`math/rand/v2`'s generic
  method) or its source (`file requires newer Go version go1.27`), so the go directive is what makes CI's `actions/setup-go` install a Go that can.
  Depth: `docs/CI.md`
- CLI framework: `github.com/spf13/cobra`
- Test parsing: `gotest.tools/gotestsum/testjson`
- Test assertions: upstream `github.com/stretchr/testify` (`assert`/`require`) — the in-house `wow-look-at-my/testify` fork has been removed; the
  `testifycast` analyzer supplies the fork's loose cross-type numeric equality via explicit conversions
- No Makefile — use `go run ./src` as the build entry point
- Binaries are output to `build/` directory
- Platform-specific files use `_linux.go`, `_darwin.go`, `_windows.go`, `_cosmo.go` suffixes (see `src/test/xattr_*.go`). GOOS=cosmo (gosmopolitan fat
  APE) matches the `unix` build tag, and — since gosmopolitan's matchTag aliases GOOS=cosmo into `linux` — also matches `linux`, both by explicit
  `//go:build` tag and by the `_linux.go`/`_linux_ARCH.go` filename convention. `golang.org/x/sys/unix` therefore now builds for cosmo like any other
  linux target: reach for a plain `_linux.go` file first. **Every build is a cosmo build now, so a `!cosmo` split turns a feature OFF in every
  binary that ships** — before keeping one, compile the dependency for cosmo and confirm it still fails (`go-git` builds now; the split excluding
  it was disabling vet's auto-fix check). **Compiling is not the test, though: RUN each payload.** `modernc.org/sqlite` compiles for cosmo and its
  `modernc.org/libc` init still panics on the windows payload, killing every Windows invocation before `main` — which is why the deps cache is
  dependency-free (`depscache_file.go`). A `_cosmo.go` file is for a genuine gap only —
  `otlptracehttp` is the surviving one, via grpc's `syscall.TCP_INFO`. Either a dedicated implementation already exists
  (exclude it from the linux side with `linux && !cosmo`), or the linux side depends on a mechanism cosmo's translation layer has no equivalent for
  (vDSO syscalls, cgroup files, AF_PACKET, netlink, `SCM_CREDENTIALS`)
- **`_cosmo` in a filename is a real GOOS filter now, so a file that must build everywhere cannot carry it.** Stock Go knows no GOOS called cosmo
  and ignored the suffix, which is how `matrix_cosmo_test.go` shipped shared test helpers under a name that promised the opposite. The fork does
  know it, and the test binaries build for the host (`WithHostTarget`), so the file vanished and every caller failed `undefined`. Name a
  cosmo-flavored file that is NOT platform-specific with the word up front — `cosmomatrix_test.go`. `GOOS=linux go vet ./...` under the fork is
  what catches this; the pipeline's own vet reads the cosmo variant, where such a file is present

## Documentation

- **Always keep `README.md` up to date** when adding new features, flags, subcommands, or changing existing behavior. The README is the primary
  user-facing documentation and must accurately reflect the current state of the CLI and GitHub Action.
- When adding a new subcommand, add it to the Subcommands section and include a CLI usage example.
- When adding a new flag, add it to the appropriate flags table (persistent or command-specific).
- When changing action.yml inputs, update the Action Usage section accordingly.
- When changing the build pipeline steps (e.g. adding a new check or phase), update `docs/PIPELINE.md`.
- **The README is for a skimming human**: keep each bullet to about two rendered lines and point at `docs/` for the depth. A paragraph of internals
  in a feature bullet belongs in a doc, not in the README.
- **This file is an index; the depth lives in `docs/`.** Add depth to the doc, never to the bullet: an entry needing more than two or three lines
  wants a `docs/` file (see `docs/CMD.md`, `docs/CACHE.md`, `docs/CI.md`, `docs/ACTION.md`, `docs/VET.md`, `docs/DATS-PHASE.md`,
  `docs/AGENT-OUTPUT-GUARD.md`, `docs/WARNINGS-GATE.md`, `docs/DEPS.md`, `docs/BUILDHOST-MANIFEST.md`, `docs/PIPELINE.md`, `docs/MATRIX.md`,
  `docs/WASM.md`, `docs/MEMLIMIT.md`, `docs/PROFILE.md`, `docs/TRACING.md`, `docs/BUILD-OUTPUTS.md`, `docs/GOMOD.md`). Each entry appears exactly once — editing a bullet means
  updating it in place, never appending a second "generation" alongside the old one. Lines are hard-wrapped at 150 columns so an
  edit shows up as a reviewable diff. A literal
  double-curly-brace GitHub Actions expression (e.g. quoting `action.yml` or a workflow), in this file or under `docs/`, must be escaped for Jekyll's
  Liquid engine (wrap it with raw/endraw tags) or `pages build and deployment` hard-fails parsing it as a template tag on unbalanced braces.

## Known Issues

- **`TestStatsStreaming` (src/cache/cache_test.go)** — RESOLVED. Root cause found and fixed: a unix-socket `connect(2)` completes as soon as the
  kernel
  queues the connection, before userspace `accept(2)`, so "dial succeeded" never implied a reader existed. `StatsListener.Close` granted only a 10ms
  accept-queue grace (via a listener deadline), and once Go's poller published the expired deadline the queued connection was discarded together with
  every buffered stat event (`Puts=0` under heavy load, when the accept goroutine was starved past the window). Fixed with an accept-side ack
  handshake: the accept loop writes a 1-byte ack after registering the connection in its WaitGroup, and dialers (`NewServer`, `NewDaemon`) only keep
  the stats connection after reading that ack (5s deadline; on timeout they close the conn and run stats-off). `Close`'s `wg.Wait()` is now a real
  happens-before edge; the 10ms drain remains as belt-and-suspenders for a dialer racing Close itself.
