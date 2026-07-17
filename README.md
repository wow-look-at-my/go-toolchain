# go-toolchain

A GitHub Action and CLI tool that builds Go projects with test coverage enforcement. Builds are gated on meeting a minimum coverage threshold, ensuring code quality doesn't regress.

## Features

- **Coverage enforcement** — fails the build if test coverage drops below 80%; emits a `::error::` workflow annotation in GitHub Actions so the failure is tagged in the run UI
- **Coverage watermarking** — optionally locks in a coverage floor using filesystem extended attributes, preventing regressions (with a 2.5% grace period)
- **Cross-compilation** — build for multiple OS/architecture combinations in parallel via the `matrix` subcommand
- **Benchmarks** — benchmarks run automatically after builds; compare against previous results stored in git notes
- **Near-duplicate detection** — scans Go source for structurally similar functions using AST comparison
- **File length checks** — warns at 500 lines, errors at 750 lines; generated files (canonical `// Code generated ... DO NOT EDIT.` header marker) are auto-exempted unless `--count-generated` is passed
- **Auto-fix / CI check** — locally (`CI` unset) it fixes linter violations and the migrations below in place; on CI (`CI` set) the very same checks run read-only and a tree that isn't already canonical is a hard build failure (listing the offending files and the local remedy), so CI can never pass green on something the local autofixer would have rewritten
- **testify upstream migration** — rewrites in-house `github.com/wow-look-at-my/testify` imports back to upstream `github.com/stretchr/testify` (and migrates `gotest.tools` likewise), then inserts explicit type conversions into `assert`/`require` `Equal`/`NotEqual` and ordering (`Greater`/`GreaterOrEqual`/`Less`/`LessOrEqual`) operands so cross-type numeric comparisons the fork's loose comparisons accepted keep compiling and passing against upstream (which is type-strict on both paths — its ordering assertions fail cross-kind operands with "Elements should be the same type") — e.g. `assert.Equal(t, 0, f)` with `f float64` becomes `assert.Equal(t, float64(0), f)`, and `assert.Greater(t, v, 0)` with `v int16` becomes `assert.Greater(t, v, int16(0))`. The conversion is type-aware (only inserted when sound) and idempotent, and the vendor tree is resynced so vendored repos stay buildable with `-mod=vendor`
- **Go generate** — detects and runs `//go:generate` directives with hash-based approval
- **Dependency checking** — detects outdated dependencies and auto-updates same-org deps
- **Dependency graph submission** — automatically submits a dependency snapshot to GitHub's Dependency Submission API in CI, populating the repository's dependency graph for vulnerability alerts and Dependabot
- **Self-update** — update the binary in place via the `update` subcommand, or enable automatic enforced updates with `GO_TOOLCHAIN_AUTO_UPDATE=1` so a stale binary baked into an image heals itself before it runs
- **Automatic GOMEMLIMIT** — injects a tiny, stdlib-only startup guard (`gomemlimit_gen.go`) into every `main` package it builds, so each binary reads its cgroup memory limit (v2 or v1) and sets `GOMEMLIMIT` to 90% of it, keeping the Go GC under the container ceiling instead of allocating until the kernel OOM-kills it. The guard is a transient build artifact — injected just before the build and removed right after, so it never lingers in the working tree or shows up as an uncommitted change; it is also listed in the repo's clone-local `.git/info/exclude` at inject time, so Go's own version stamping never sees it as an untracked file and built binaries keep clean `+dirty`-free provenance. It adds no dependency, carries the standard generated-code marker (so it never counts against coverage), and is a no-op when no limit is found or off-Linux. Defers to an explicit `GOMEMLIMIT` (`GOMEMLIMIT=off` is a per-deploy kill switch); disable injection entirely with `GO_TOOLCHAIN_AUTO_MEMLIMIT=off`
- **Output stall watchdog** — the build's stdout/stderr are routed through an in-process watchdog that prints a loud `STALLED: no output for Ns` warning (with the current step name) whenever the pipeline goes silent for 5+ seconds. Disable it with `GO_TOOLCHAIN_NO_WATCHDOG=1` — the build then runs on its real stdio (useful when debugging output plumbing, since the watchdog works by dup2-redirecting fd 1/2 through pipes)
- **CPU profiling** — run benchmarks with pprof profiling via the `profile` subcommand
- **Local install** — install the binary to `~/.local/bin` via the `install` subcommand
- **Coverage impact metrics** — each package/file/function shows how many percentage points it costs the total, making it easy to prioritize what to test next
- **Colorized output** — coverage percentages displayed with a red-to-green color gradient
- **CI summary** — automatically writes a rich GitHub Step Summary with test results, source links, coverage, benchmark comparisons, and a Mermaid Gantt chart of the pipeline timeline when running in GitHub Actions
- **Web-backed build cache** — GOCACHEPROG protocol server with local and web backends for shared build caching across CI runs (Go 1.24+). Uses server-side batch GET with prefetch: the server returns requested entries plus temporally related entries from the same build, proactively populating the local cache. The **local tier is a FUSE virtual filesystem**: cached object bodies are stored in append-only pack files and served on demand through a read-only mount, so a build cache that would otherwise be thousands of tiny files (two per entry) collapses to a handful of packs — with a graceful fallback to a loose-file cache when FUSE is unavailable (or forced off with `GOCACHE_NO_FUSE=1`). Every cached body is CRC-verified before it is served — on both the GET RPC and the mount read path the compiler actually uses — so a corrupted entry is treated as a miss and recomputed rather than handed to the toolchain. Objects pulled from the shared remote cache are additionally verified end to end (the body must hash to its advertised `outputID`) and, for compiled packages, cross-checked against the **build id** stamped in the archive, so an object served under the wrong action key — the cause of confusing failures like `"runtime" imported as reflectlite` — is rejected as a miss instead of poisoning the build. The remote tier is also **fail-safe under outages**: any 5xx/timeout/reset degrades to a clean cache miss (build from source — slower, still correct), transient errors get bounded retries with jittered backoff, and after a run of consecutive failures a **circuit breaker** trips the backend to local-only for the rest of the run (logged once) so a struggling cache server is never hammered by a build's thousands of cache ops. Tunable via `GO_TOOLCHAIN_CACHE_BREAKER_THRESHOLD` (default 24; `0` disables) and `GO_TOOLCHAIN_CACHE_MAX_RETRIES` (default 2; `0` disables). See [docs/CACHE.md](docs/CACHE.md) for the architecture, diagrams, and on-disk format
- **Vanity URL resolution** — automatically detects and resolves vanity-URL module dependencies via Go proxy or go-import meta tags
- **Go proxy/sumdb support** — reads `GO_PROXY_CONFIG` (base64 JSON) to configure proxy URL, credentials (via ~/.netrc), and sumdb key automatically
- **Generated code exclusion** — automatically detects files with the standard `// Code generated ... DO NOT EDIT.` marker and excludes them from both test execution and coverage calculations (e.g. sqlc, protobuf, mockgen output)
- **Release management** — create GitHub releases with checksums, structured release notes, and rolling tag management via the `release` subcommand
- **Buildhost publishing** — CI automatically publishes cross-compiled binaries to [buildhost](https://pazer.build) via OIDC, making them available for download in multiple formats (raw binary, tar.gz, deb, Homebrew, npm, OCI)
- **Background update check** — every run kicks off a non-blocking check against [buildhost](https://pazer.build) for a newer published `go-toolchain`. If this binary's commit is behind the latest release, a one-line warning is logged at the end of the run. The check runs in the background and is killed the instant the build finishes — it never blocks, delays, or fails the build, and stays silent on any error (offline, 404, etc.). It is a check only: the binary never replaces itself (update via buildhost/Homebrew/npm/APT). The check always runs — there is no opt-out. Point it at a self-hosted buildhost with `GO_TOOLCHAIN_BUILDHOST_URL`; it is skipped only for the `version` command (which reports its own staleness) and the GOCACHEPROG subprocess
- **Claude output guard** — when go-toolchain runs under the Claude agent (detected via the `CLAUDECODE` environment marker and process ancestry), it refuses to run unless its full output is visible in the agent's transcript. Every way of hiding or truncating that output aborts immediately with an error pointing back to a plain run: piping into another command (`head`/`tail`/`grep`/`sed`/`awk`/`cat`/`tee`/…), redirecting to a file (`> out.log`, `>> out.log`), discarding to `/dev/null`, or capturing via `$(...)`. The only allowed "redirect" is the harness's own transcript-capture file (recognized by the `CLAUDE_CODE_SESSION_ID` embedded in its path) — that *is* how the agent reads the output — and a real terminal. It is a no-op when not running under Claude, so CI and human shells are unaffected. The guard is unconditional — there is deliberately no environment variable or flag to disable it. Stdout classification uses `/proc`, so the guard is live on linux hosts — including the released binaries, which are GOOS=cosmo fat-APE copies (the classifier builds for both `linux` and `cosmo`); native darwin/windows builds, and the cosmo APE on a macOS host (no `/proc`), fail open and never fire

## GitHub Action Usage

Use the composite action in any `wow-look-at-my` org repo. Secrets are fetched automatically from [secret-server](https://github.com/wow-look-at-my/actions/tree/secret-server) via GitHub OIDC — no secret passing required:

```yaml
permissions:
  contents: write
  id-token: write          # required for secret-server and buildhost autorelease (OIDC)
  security-events: write   # required for CodeQL SARIF upload (see CodeQL note below)

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: wow-look-at-my/go-toolchain@v1
```

The action handles everything: fetching secrets, configuring the Go proxy, private repo access, web build cache, running `go-toolchain matrix`, and a CodeQL `security-and-quality` analysis around the build.

**CodeQL prerequisites** (the action runs CodeQL by default):

- The workflow must grant `security-events: write`. The action probes the SARIF upload endpoint up front and **fails fast** if this permission is missing.
- The repo must NOT have GitHub's default CodeQL setup enabled — disable it under *Settings → Code security → Code scanning → CodeQL → Default setup*. Otherwise SARIF uploads fail with `"CodeQL analyses from advanced configurations cannot be processed when the default setup is enabled"`.

To opt out, pass `codeql: 'false'`.

### Inputs

| Input               | Type     | Default    | Description                                              |
|---------------------|----------|------------|----------------------------------------------------------|
| `json`              | string   | `false`    | Output coverage report as JSON                           |
| `generate`          | string   | `''`       | Run `go:generate` directives matching this hash          |
| `working-directory` | string   | `.`        | Working directory for the build                          |
| `binary`            | string   | `''`       | Path to a pre-built go-toolchain binary (skips release download) |
| `os`                | string   | `linux,darwin,windows` | Comma-separated target operating systems; `wasm` (WebAssembly) pairs only with arch `js`/`wasip1` — see [WebAssembly targets](#webassembly-targets---targets-wasmjswasmwasip1) |
| `arch`              | string   | `amd64,arm64` | Comma-separated target architectures; the wasm flavors `js`/`wasip1` pair only with os `wasm` |
| `targets`           | string   | `''`       | Comma-separated exact build targets, each an `os/arch` pair (e.g. `darwin/amd64`, or `wasm/js`/`wasm/wasip1` for WebAssembly — see [WebAssembly targets](#webassembly-targets---targets-wasmjswasmwasip1)) or the special value `cosmo` (one gosmopolitan fat APE plus per-platform slot copies — see [Cosmopolitan fat binaries](#cosmopolitan-fat-binaries---targets-cosmo)). When non-empty this replaces the `os`/`arch` inputs |
| `cgo`               | string   | `false`    | Enable CGO (disabled by default for static binaries) |
| `autorelease`       | string   | `true`     | Automatically publish to buildhost on every branch push (requires `id-token: write`) — publishes the `build/` directory directly from the workspace, no GitHub Actions artifact involved |
| `allow-source-build` | string  | `false`    | Allow building go-toolchain from source when the buildhost binary is unavailable; when `false`, the build fails fast instead of silently falling back |
| `timeout`           | string   | `10`       | Timeout in minutes for the go-toolchain build step |
| `wait-ci`           | string   | `false`    | Wait for the latest go-toolchain CI run before downloading the release binary |
| `codeql`            | string   | `true`     | Run CodeQL `security-and-quality` analysis around the build (see prerequisites above) |

## CLI Usage

```bash
# Install
go install github.com/wow-look-at-my/go-toolchain@latest

# Run tests and build (default workflow)
go-toolchain

# Cross-compile for multiple platforms
go-toolchain matrix --os linux,darwin,windows --arch amd64,arm64

# Build an exact target list: one Cosmopolitan fat APE plus three native builds
go-toolchain matrix --targets cosmo,darwin/amd64,darwin/arm64,windows/arm64

# WebAssembly builds (browser/Node.js and WASI) alongside a native target
go-toolchain matrix --targets wasm/js,wasm/wasip1,linux/amd64

# Run benchmarks independently
go-toolchain bench run --benchtime 5s --count 3

# Save benchmark results to git notes
go-toolchain bench save

# Compare benchmarks between commits
go-toolchain bench compare HEAD~3 HEAD

# Detect near-duplicate code
go-toolchain lint ./...

# Install binary to ~/.local/bin
go-toolchain install

# Self-update to latest release
go-toolchain update

# Show version and staleness info
go-toolchain version

# Print just the version number
go-toolchain version raw

# Print version info as JSON
go-toolchain version json

# Create a GitHub release with checksums
go-toolchain release --tag v1.0.0
```

### Flags

#### Persistent flags (shared across subcommands)

| Flag             | Default     | Description                                          |
|------------------|-------------|------------------------------------------------------|
| `--json`         | `false`     | Output coverage as JSON                              |
| `-v`, `--verbose` | `false`    | Verbose output: debug log level, plus per-test output lines |
| `--log-level`    | `info`      | Minimum log level: `debug`, `info`, `warn`, `error`, or `silent`. Precedence: `--log-level` > `--verbose` > `GOCACHE_DEBUG=1` (debug) |
| `--generate`     | `''`        | Run `go:generate` directives matching this hash      |
| `--threshold`    | `0.75`      | Similarity threshold for duplicate detection (0.0-1.0) |
| `--min-nodes`    | varies      | Minimum AST node count for duplicate detection       |
| `--cgo`          | `false`     | Enable CGO (disabled by default for static binaries) |

Log routing: debug messages go to stderr and info to stdout; warnings and errors print to stderr locally and are emitted as `::warning`/`::error` workflow annotations in GitHub Actions, so they surface in the run UI (multi-line messages are escaped per the workflow-command encoding, so they annotate intact).

#### Root command flags

| Flag              | Default | Description                                                    |
|-------------------|---------|----------------------------------------------------------------|
| `--no-benchmark`  | `false` | Skip benchmarks after build                                    |
| `--benchtime`     | `''`    | Duration or count for each benchmark (e.g. `5s`, `1000x`)     |
| `-n`, `--count`   | `1`     | Number of times to run each benchmark                          |
| `--cpu`           | `''`    | GOMAXPROCS values to test with (comma-separated)               |

### Subcommands

- **`matrix`** — cross-compile for multiple platforms (`--os`, `--arch`, `--parallel`, `--no-benchmark`)
- **`bench`** — run and manage benchmarks
  - `run` — run benchmarks and show deltas vs stored results
  - `save` — run benchmarks and store results in git notes
  - `show [commit]` — show stored benchmark results (default: HEAD)
  - `compare <commit1> <commit2>` — compare benchmark results between two commits
- **`lint`** — detect near-duplicate code blocks using AST comparison
- **`install`** — install the binary to `~/.local/bin`
- **`update`** — self-update to the latest GitHub release
- **`release`** — create a GitHub release with checksums and structured release notes (`--tag`, `--from`, `--build`)
- **`version`** — show build version and staleness information
  - `raw` — print just the version number
  - `json` — print version info as JSON (version, commit, dates, staleness)

### Automatic updates

Setting `GO_TOOLCHAIN_AUTO_UPDATE=1` makes go-toolchain check for a newer release
before each real build and, if one exists, update itself in place and re-execute
on the new binary. This is intended for environments that ship a fixed, possibly
stale binary (e.g. the Claude Code web image): an outdated binary heals itself on
first use instead of silently running stale build logic.

```bash
# Opt in (e.g. in an image's environment)
export GO_TOOLCHAIN_AUTO_UPDATE=1
go-toolchain        # updates if behind, then re-runs on the new binary
```

The check **fails open** — a flaky or unreachable release registry prints a
warning and continues on the current binary rather than blocking the build. Set
the variable to `0`/`false`/`no`/`off` (or leave it unset) to disable. The
`update`, `version`, `install`, `release`, and `cacheprog` subcommands are never
auto-updated.

### WebAssembly targets (`--targets wasm/js,wasm/wasip1`)

`matrix --targets` also accepts the two WebAssembly platforms: `wasm/js`
(browser / Node.js, run with `wasm_exec.js`) and `wasm/wasip1` (WASI runtimes
such as wasmtime or wazero) — spelled os-first to match buildhost's wasm
artifact scheme and the `<name>_wasm_js` artifact naming. The GOOS-order
spellings `js/wasm` and `wasip1/wasm` are accepted as compatibility aliases
and normalize to the same targets (mixing both spellings dedupes to one
target). Wasm targets mix freely with native pairs and `cosmo` in one run:

```bash
go-toolchain matrix --targets wasm/js,wasm/wasip1,linux/amd64
```

The same pairing also works through the `--os`/`--arch` cartesian product
(and thus the action's `os:`/`arch:` inputs): `--os wasm` combines only with
the wasm flavor arches `js`/`wasip1`, producing the identical targets —
`--os wasm --arch js` is `--targets wasm/js` (same artifacts, naming, and
per-target main discovery). In a mixed list the impossible cross
combinations (`wasm` with a native arch, a native os with `js`/`wasip1`) are
skipped with one aggregate warning; if the whole product is impossible
(`--os wasm --arch amd64` alone) the build fails fast, and a `js`/`wasip1`
arch without `wasm` anywhere in `--os` is an error naming the fix. A
wasm-only consumer's action config is simply:

```yaml
with:
  os: wasm
  arch: js
```

**Per-target main-package discovery.** With an explicit `--targets` list,
main packages are discovered under **each target's own build context**
(GOOS/GOARCH), not the host's: a main package guarded `//go:build js && wasm`
(e.g. a browser entry point importing `syscall/js`) is built for `wasm/js`
targets and never attempted for native ones, an unconstrained main builds for
every target as before, and a `//go:build linux` main builds for `linux/*`
entries even from a non-linux host. A target whose context has no main
packages at all is skipped with a warning (a target list where **no** entry
has any main packages is still an error). The `cosmo` pseudo-target keeps
host-context discovery (the fat APE spans several native platforms), and the
legacy `--os` x `--arch` product keeps host-context discovery exactly as
before.

**Toolchain.** Wasm targets are built with the same
[gosmopolitan](https://github.com/wow-look-at-my/gosmopolitan) fork toolchain
as the cosmo target (resolution is identical: `GO_TOOLCHAIN_COSMO_GOROOT`,
else a buildhost download selected by `GO_TOOLCHAIN_COSMO_BRANCH`, cached
under `~/.cache/go-toolchain/cosmo/`) — the fork carries this org's wasm
runtime fixes (default-on preemptible loops, Node.js `fetch` networking,
synchronous stdout under node, CPU profiling, DWARF debug info; see the fork's
`WASM_SHORTCOMINGS.md`). The fork defaults to `GOOS=cosmo`, so wasm builds
always pin `GOOS`/`GOARCH` explicitly and run with `GOTOOLCHAIN=local` and
`CGO_ENABLED=0` (wasm has no cgo; `--cgo` warns and is ignored for these
targets).

**Artifacts.** Wasm binaries are named `<name>_wasm_js` /
`<name>_wasm_wasip1` — buildhost's wasm artifact convention (`os=wasm` with
`arch=js`/`arch=wasip1`), with the order deliberately swapped relative to
`GOOS_GOARCH` and **no file extension**: the publish pipeline parses
artifacts from the trailing two underscore-separated filename tokens after
stripping only `.exe`, so the bare form is what publishes as
`os=wasm`/`arch=js|wasip1` (an extension would keep the file out of the
upload set entirely). The files are still ordinary wasm modules, covered by
`checksums.txt`; none of the cosmo slot machinery applies to them.

**Buildhost publishing.** By default wasm artifacts are published to
buildhost like any other target, as `os=wasm` with `arch=js`/`arch=wasip1`.
This **requires a buildhost with wasm artifact support**
([buildhost#166](https://github.com/wow-look-at-my/buildhost/pull/166)); on
an older server the upload is 400-rejected (`invalid os "wasm"` — the same
validation that rejects `os=cosmo`, and that rejected the pre-convention
`os=js` naming with `invalid os "js"` in the field) and a single rejected
artifact aborts the whole publish. The build logs a warning whenever wasm
targets are built, naming the requirement and the opt-out. For consumers
whose buildhost predates wasm support, set **`GO_TOOLCHAIN_WASM_PUBLISH=0`**:
wasm artifacts then take the excluded `<name>_<goos>_wasm.wasm` naming, whose
`.wasm` suffix keeps them outside the publish upload set (the same skip that
covers `checksums.txt` and `profile.json`) while the real files remain in
`build/` and `checksums.txt` for any downstream step to pick up. With the opt-out active, a **wasm-only** target list leaves the
publish step nothing to upload and it fails with "No matrix artifacts" —
disable `autorelease` in that combination (the build logs a warning for this
case too). Without the opt-out, wasm-only publishes are fine once the server
has wasm support.

**wasm_exec.js.** A `wasm/js` build also copies the fork toolchain's
`lib/wasm/wasm_exec.js` — the JS harness that loads the wasm in a browser or
Node, which must byte-match the toolchain that built it — into
`build/wasm_exec.js`. It is covered by `checksums.txt` and stays in `build/`,
but sits outside the buildhost publish set (its name doesn't match
the publish pipeline's `<binary>_{os}_{arch}` pattern, like `checksums.txt`
itself). Missing harness in the fork GOROOT only warns.

**GOMEMLIMIT guard.** The injected cgroup guard is stdlib-only and compiles
for both wasm ports; without cgroup files it is a startup no-op, so wasm
binaries are built from the same guarded source as every other target. The
guard is injected into main packages visible under the **host** context only;
a main that exists only under a cross-compile context (such as a
`js && wasm`-guarded browser entry point) gets no guard — sound, since the
guard reads Linux cgroup limits and would no-op there anyway. Discovery skips
the guard file by name, so an injected (or stale) guard never makes a
host-only main dir look like a main package for another target.

**Running and testing wasm binaries.** The build pipeline never executes
matrix artifacts, and the test phase always runs on the HOST platform — wasm
builds do not change what `go test` tests. To run the artifacts or execute a
package's tests under wasm, use the fork toolchain's exec wrappers in
`<goroot>/lib/wasm` (`go_js_wasm_exec` needs Node.js 18+; `go_wasip1_wasm_exec`
needs wasmtime, or wazero via `GOWASIRUNTIME=wazero`):

```bash
GOROOT=$HOME/.cache/go-toolchain/cosmo/<key>/go
PATH="$GOROOT/bin:$GOROOT/lib/wasm:$PATH" GOTOOLCHAIN=local \
  GOOS=js GOARCH=wasm go test ./...
```

Rejected spellings fail fast with a pointer to the right one: `js`/`wasip1`
in `--os` and `wasm` in `--arch` (both flipped in buildhost's model — use
`--os wasm --arch js|wasip1`, or `--targets wasm/js`/`wasm/wasip1`),
`js/amd64`, `linux/wasm` and `wasm/amd64` (impossible pairings), a
`js`/`wasip1` arch with no `wasm` os in the list, and wasm targets in
`--cosmo-slots` (an APE is not a wasm binary).

### Automatic GOMEMLIMIT (cgroup-aware memory limit)

By default, go-toolchain injects a small, stdlib-only startup guard
(`gomemlimit_gen.go`) into every `main` package it builds. When the resulting
binary starts, the guard reads the container's cgroup memory limit (cgroup v2 or
v1) and calls `runtime/debug.SetMemoryLimit` with 90% of it. This keeps the Go
garbage collector under the cgroup ceiling — as the heap approaches the limit
the GC works harder, trading CPU for memory, instead of letting the process
allocate until the kernel OOM-kills it.

The guard is a **transient build artifact**: go-toolchain writes it into each
`main` package immediately before compiling and removes it again as soon as the
build is done, so it never lingers in your working tree and never needs to be
committed. The CI dirty-tree check ignores `gomemlimit_gen.go` in every git
state — added, modified, or deleted — so neither the in-flight guard nor a copy
left behind by an interrupted build ever fails a build. Before injecting,
go-toolchain also lists `gomemlimit_gen.go` in the repository's clone-local
`.git/info/exclude` (idempotently; the entry stays), so the guard is invisible
to `git status` for the whole build window — that matters because Go's own
main-module version stamping (Go 1.24+) checks `git status` at build time, and
an untracked guard used to make every built binary stamp its version `+dirty`
even on a perfectly clean checkout. The exclude file lives under `.git/`,
outside your working tree, so the entry itself can never show up as a change
(which is exactly why the guard is not added to `.gitignore` instead). The
guard is dependency-free (no `go.mod`/`go.sum` changes), carries the standard
`// Code generated ... DO NOT EDIT.` marker (so it is excluded from coverage),
is idempotent, and is a no-op when no cgroup limit is found, including on
non-Linux systems.

If a repository committed the guard under an older go-toolchain, the cleanup
deletes those files from the working tree on the next run (without failing the
build); commit that deletion once to drop the stale files for good.

```bash
# Build-time: disable injection (default is on)
export GO_TOOLCHAIN_AUTO_MEMLIMIT=off
```

The following are read by the built program **when it starts**, not at build time:

```bash
# Opt a single deployment out without rebuilding — Go's own variable wins
export GOMEMLIMIT=off

# ...or pin an explicit limit (the guard then does nothing)
export GOMEMLIMIT=2GiB

# Tune the headroom ratio (default 0.9); "off" also disables the guard
export GO_TOOLCHAIN_MEMLIMIT_RATIO=0.8
```

## OpenTelemetry Trace Export

go-toolchain can export build pipeline timings as OpenTelemetry traces, enabling visualization in Grafana Tempo or any OTLP-compatible backend.

Trace export is controlled entirely by standard `OTEL_*` environment variables. When `OTEL_EXPORTER_OTLP_ENDPOINT` is unset, no traces are exported and there is zero overhead.

```bash
# Export traces to a local collector
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 go-toolchain

# Export to Grafana Cloud Tempo
OTEL_EXPORTER_OTLP_ENDPOINT=https://tempo-us-central1.grafana.net \
OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic $(echo -n 'user:api-key' | base64)" \
go-toolchain
```

**Span hierarchy:**
- Root span `go-toolchain` covering the entire build
  - Worker spans, all named `build.worker`, distinguished by the `build.worker.id` attribute (`main`, `deps`, `worker-1`, etc.)
    - Step spans (e.g., `go mod tidy`, `go vet ./...`). Cross-compile steps collapse into a static `build.compile` span carrying `build.target.os` and `build.target.arch` attributes (e.g. `linux`/`amd64`) instead of encoding the platform in the span name.

All spans use `INTERNAL` kind. Success and failure are reported via span status (`OK` / `ERROR`) rather than boolean attributes. Resource attributes include `github.sha`, `github.repository`, `github.ref`, and `github.run_id` when running in GitHub Actions.

`cacheprog` also exports a `cacheprog.http_error` span for each HTTP error from the remote cache (`web put`, `web get`, `web batch get`), with attributes `cacheprog.op`, `http.response.status_code`, `cacheprog.action_id`, and a truncated `cacheprog.body`. When OTel is not configured, these spans are skipped. Stderr only emits an aggregated summary line per (operation, status, body) at most every 30 seconds (plus one final flush at shutdown), so a flaky remote no longer floods the terminal with one line per failed request.

## How It Works

1. Configures Go proxy and sumdb environment (via `GO_PROXY_CONFIG` or env vars)
2. Checks for outdated dependencies (auto-updates same-org deps)
3. Resolves vanity-URL module dependencies (injects replace directives for unreachable hosts)
4. Runs `go mod tidy`
5. Detects and runs `//go:generate` directives (if present)
6. Runs `go vet`: custom analyzers normalize assertions, migrate `gotest.tools`/fork-testify imports to upstream `stretchr/testify`, and insert explicit type conversions into cross-type `assert`/`require` `Equal`/`NotEqual` and `Greater`/`Less`-family operands (resyncing the vendor tree afterward). Locally these fixes are applied in place; on CI (`CI` set) they run read-only and any change they would make is a hard failure instead — so importing the removed `wow-look-at-my/testify` fork fails CI rather than passing green
7. Checks for near-duplicate code blocks (warnings only)
8. Checks file lengths (warns at 500 lines, errors at 750)
9. Starts GOCACHEPROG server with local + web backends (if web cache credentials are configured). The local tier is a FUSE virtual filesystem backed by append-only pack files (see [docs/CACHE.md](docs/CACHE.md)); a cache hit returns a `DiskPath` into the mount and the kernel serves the body on demand from a pack, so no per-entry loose files or sidecars are written. Cache misses use the server's batch GET endpoint with prefetch — the server returns the requested entry plus temporally related entries from the same build, proactively populating the local cache. PUTs upload entries individually with LZ4 compression. Each object is tagged with metadata headers describing what it is:
   - `Object-Type` — file type detected from magic bytes (`go-archive`, `elf-binary`, `macho-binary`, `pe-binary`, `go-object`, or `unknown`)
   - `Go-Version` — the Go compiler version that produced the artifact (e.g. `go1.24.7`), extracted from Go archive headers
   - `Target` — the target platform (e.g. `linux/amd64`), extracted from Go archive headers
   - `Body-Size` — original uncompressed size in bytes
   - `Compression` — compression algorithm (`lz4`)
   - `Toolchain-Version` — the go-toolchain version that cached the entry
   - `Created` — RFC 3339 timestamp of when the entry was first cached
10. Discovers packages with test files, excluding those where all non-test `.go` files are generated code
11. Runs `go test` across non-generated packages with coverage profiling
12. Filters generated files from coverage profile, then displays per-item impact and compares against the minimum threshold (80%, or watermark - 2.5%)
13. Reports cache size breakdown (Go build cache, toolchain downloads, module cache) when running in GitHub Actions
14. If coverage meets the threshold, injects the cgroup→`GOMEMLIMIT` startup guard into each `main` package (unless `GO_TOOLCHAIN_AUTO_MEMLIMIT=off`) — first listing it in the clone-local `.git/info/exclude` so `git status`, and with it Go's build-time version stamping, never sees the transient file (binaries stamp clean provenance instead of `+dirty`) — builds the project binaries into `build/`, then removes the transient guard files so they never linger in the working tree (the dirty-tree check ignores `gomemlimit_gen.go` in every git state)
15. Automatically adds `build/` to `.gitignore` (if in a git repo)
16. Runs benchmarks and compares against previously stored results
17. Submits a dependency snapshot to GitHub's Dependency Submission API (when `$CI` and `$GITHUB_REPOSITORY` are set), populating the repository's dependency graph with all direct and indirect Go module dependencies for vulnerability scanning and Dependabot alerts
18. Writes a GitHub Step Summary (when `$GITHUB_STEP_SUMMARY` is set) with a test case table, clickable source links, coverage stats, benchmark comparison, and a Gantt chart showing the pipeline timeline across all threads

## Development

```bash
# Run the tool on itself
go run ./src

# Build and test (runs mod tidy, vet, tests with coverage, then builds)
go-toolchain
```

## License

See repository for license details.
