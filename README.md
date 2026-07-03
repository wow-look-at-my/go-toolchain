# go-toolchain

A GitHub Action and CLI tool that builds Go projects with test coverage enforcement. Builds are gated on meeting a minimum coverage threshold, ensuring code quality doesn't regress.

## Features

- **Coverage enforcement** — fails the build if test coverage drops below 80%; emits a `::error::` workflow annotation in GitHub Actions so the failure is tagged in the run UI
- **Coverage watermarking** — optionally locks in a coverage floor using filesystem extended attributes, preventing regressions (with a 2.5% grace period)
- **Cross-compilation** — build for multiple OS/architecture combinations in parallel via the `matrix` subcommand
- **Benchmarks** — benchmarks run automatically after builds; compare against previous results stored in git notes
- **Near-duplicate detection** — scans Go source for structurally similar functions using AST comparison
- **File length checks** — warns at 500 lines, errors at 750 lines (with exemption support)
- **Auto-fix / CI check** — locally (`CI` unset) it fixes linter violations and the migrations below in place; on CI (`CI` set) the very same checks run read-only and a tree that isn't already canonical is a hard build failure (listing the offending files and the local remedy), so CI can never pass green on something the local autofixer would have rewritten
- **testify upstream migration** — rewrites in-house `github.com/wow-look-at-my/testify` imports back to upstream `github.com/stretchr/testify` (and migrates `gotest.tools` likewise), then inserts explicit type conversions into `assert`/`require` `Equal`/`NotEqual` operands so cross-type numeric comparisons that the fork's loose `ObjectsAreEqual` accepted keep compiling and passing against upstream — e.g. `assert.Equal(t, 0, f)` with `f float64` becomes `assert.Equal(t, float64(0), f)`. The conversion is type-aware (only inserted when sound) and idempotent, and the vendor tree is resynced so vendored repos stay buildable with `-mod=vendor`
- **Go generate** — detects and runs `//go:generate` directives with hash-based approval
- **Dependency checking** — detects outdated dependencies and auto-updates same-org deps
- **Dependency graph submission** — automatically submits a dependency snapshot to GitHub's Dependency Submission API in CI, populating the repository's dependency graph for vulnerability alerts and Dependabot
- **Automatic GOMEMLIMIT** — injects a tiny, stdlib-only startup guard (`gomemlimit_gen.go`) into every `main` package it builds, so each binary reads its cgroup memory limit (v2 or v1) and sets `GOMEMLIMIT` to 90% of it, keeping the Go GC under the container ceiling instead of allocating until the kernel OOM-kills it. The guard is a transient build artifact — injected just before the build and removed right after, so it never lingers in the working tree or shows up as an uncommitted change. It adds no dependency, carries the standard generated-code marker (so it never counts against coverage), and is a no-op when no limit is found or off-Linux. Defers to an explicit `GOMEMLIMIT` (`GOMEMLIMIT=off` is a per-deploy kill switch); disable injection entirely with `GO_TOOLCHAIN_AUTO_MEMLIMIT=off`
- **CPU profiling** — run benchmarks with pprof profiling via the `profile` subcommand
- **Local install** — install the binary to `~/.local/bin` via the `install` subcommand
- **Coverage impact metrics** — each package/file/function shows how many percentage points it costs the total, making it easy to prioritize what to test next
- **Colorized output** — coverage percentages displayed with a red-to-green color gradient
- **CI summary** — automatically writes a rich GitHub Step Summary with test results, source links, coverage, benchmark comparisons, and a Mermaid Gantt chart of the pipeline timeline when running in GitHub Actions
- **Automatic Go toolchain bootstrap** — reads the Go version required by `go.mod` (preferring the `toolchain` directive) and, when the system Go is missing or older than required, downloads a known-good toolchain to `~/.cache/go-toolchain` and points the build at it. The preinstalled toolchain is also **integrity-checked** before use via a sub-second `go list runtime` probe under `GOTOOLCHAIN=local`: a fraction of GitHub-hosted runners ship a half-extracted Go whose binary runs and reports its version correctly but whose `GOROOT` standard library is incomplete, so the first real compile dies with `package runtime is not in std`. go-toolchain detects this corruption and re-downloads a clean Go for the required version instead of converting per-runner infrastructure rot into a hard build failure (the freshly downloaded toolchain is re-probed as a sanity check)
- **Web-backed build cache** — GOCACHEPROG protocol server with local and web backends for shared build caching across CI runs (Go 1.24+). Uses server-side batch GET with prefetch: the server returns requested entries plus temporally related entries from the same build, proactively populating the local cache. The **local tier is a FUSE virtual filesystem**: cached object bodies are stored in append-only pack files and served on demand through a read-only mount, so a build cache that would otherwise be thousands of tiny files (two per entry) collapses to a handful of packs — with a graceful fallback to a loose-file cache when FUSE is unavailable (or forced off with `GOCACHE_NO_FUSE=1`). Every cached body is integrity-verified before it is served — the GET RPC checks a CRC (post-storage rot), while the mount read path the compiler actually uses verifies the body's SHA-256 against its content address (`outputID`), which also catches a torn or mis-mapped record that is self-consistent with its own CRC — so a bad entry is treated as a miss and recomputed rather than handed to the toolchain. Objects pulled from the shared remote cache are additionally verified end to end (the body must hash to its advertised `outputID`) and, for compiled packages, cross-checked against the **build id** stamped in the archive, so an object served under the wrong action key — the cause of confusing failures like `"runtime" imported as reflectlite` — is rejected as a miss instead of poisoning the build. The remote tier is also **fail-safe under outages**: any 5xx/timeout/reset degrades to a clean cache miss (build from source — slower, still correct), and transient errors get bounded retries with jittered backoff that honor the server's `Retry-After` (the admission-control 503 shed's "wait," not "give up"). This is the only backpressure handling — there is no client circuit breaker: a remote GET/PUT is always attempted, and a failure that outlasts the retry budget falls back to a per-operation local miss rather than disabling the remote tier for the rest of the run. Tunable via `GO_TOOLCHAIN_CACHE_MAX_RETRIES` (default 2; `0` disables). See [docs/CACHE.md](docs/CACHE.md) for the architecture, diagrams, and on-disk format
- **Vanity URL resolution** — automatically detects and resolves vanity-URL module dependencies via Go proxy or go-import meta tags
- **Go proxy/sumdb support** — reads `GO_PROXY_CONFIG` (base64 JSON) to configure proxy URL, credentials (via ~/.netrc), and sumdb key automatically
- **Generated code exclusion** — automatically detects files with the standard `// Code generated ... DO NOT EDIT.` marker and excludes them from both test execution and coverage calculations (e.g. sqlc, protobuf, mockgen output)
- **Release management** — create GitHub releases with checksums, structured release notes, and rolling tag management via the `release` subcommand
- **Buildhost publishing** — CI automatically publishes cross-compiled binaries to [buildhost](https://pazer.build) via OIDC, making them available for download in multiple formats (raw binary, tar.gz, deb, Homebrew, npm, OCI)
- **Background update check** — every run kicks off a non-blocking check against [buildhost](https://pazer.build) for a newer published `go-toolchain`. If this binary's commit is behind the latest release, a one-line warning is logged at the end of the run. The check runs in the background and is killed the instant the build finishes — it never blocks, delays, or fails the build, and stays silent on any error (offline, 404, etc.). It is a check only: the binary never replaces itself (update via buildhost/Homebrew/npm/APT). The check always runs — there is no opt-out. Point it at a self-hosted buildhost with `GO_TOOLCHAIN_BUILDHOST_URL`; it is skipped only for the `version` command (which reports its own staleness) and the GOCACHEPROG subprocess
- **Claude output guard** — when go-toolchain runs under the Claude agent (detected via the `CLAUDECODE` environment marker and process ancestry), it refuses to run unless its full output is visible in the agent's transcript. Every way of hiding or truncating that output aborts immediately with an error pointing back to a plain run: piping into another command (`head`/`tail`/`grep`/`sed`/`awk`/`cat`/`tee`/…), redirecting to a file (`> out.log`, `>> out.log`), discarding to `/dev/null`, or capturing via `$(...)`. The only allowed "redirect" is the harness's own transcript-capture file (recognized by the `CLAUDE_CODE_SESSION_ID` embedded in its path) — that *is* how the agent reads the output — and a real terminal. It is a no-op when not running under Claude, so CI and human shells are unaffected. The guard is unconditional — there is deliberately no environment variable or flag to disable it. Linux only (stdout classification uses `/proc`)

## GitHub Action Usage

Use the composite action in any `wow-look-at-my` org repo. Secrets are fetched automatically from [secret-server](https://github.com/wow-look-at-my/actions/tree/secret-server) via GitHub OIDC — no secret passing required:

```yaml
permissions:
  contents: write
  id-token: write
  security-events: write   # required for CodeQL SARIF upload (see CodeQL note below)
  actions: read            # required for buildhost autorelease (listWorkflowRunArtifacts/downloadArtifact)

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
| `os`                | string   | `linux,darwin,windows` | Comma-separated target operating systems |
| `arch`              | string   | `amd64,arm64` | Comma-separated target architectures |
| `cgo`               | string   | `false`    | Enable CGO (disabled by default for static binaries) |
| `autorelease`       | string   | `true`     | Automatically publish to buildhost on every branch push (requires `id-token: write` and `actions: read`) |
| `allow-source-build` | string  | `false`    | Allow building go-toolchain from source when the buildhost binary is unavailable; when `false`, the build fails fast instead of silently falling back |
| `upload-artifacts`  | string   | `true`     | Upload `build/` directory as a GitHub Actions artifact after building |
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
| `--generate`     | `''`        | Run `go:generate` directives matching this hash      |
| `--threshold`    | `0.75`      | Similarity threshold for duplicate detection (0.0-1.0) |
| `--min-nodes`    | varies      | Minimum AST node count for duplicate detection       |
| `--cgo`          | `false`     | Enable CGO (disabled by default for static binaries) |

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
- **`release`** — create a GitHub release with checksums and structured release notes (`--tag`, `--from`, `--build`)
- **`version`** — show build version and staleness information
  - `raw` — print just the version number
  - `json` — print version info as JSON (version, commit, dates, staleness)

### Automatic GOMEMLIMIT (cgroup-aware memory limit)

By default, go-toolchain injects a small, stdlib-only startup guard
(`gomemlimit_gen.go`) into every `main` package it builds. Main-package
discovery honors build constraints, so a `//go:build ignore` `package main`
generator file (the common `go run gen.go` idiom) sitting next to a real
non-main package is correctly skipped — it is not mistaken for the directory's
main package, so the guard is never injected into a non-main directory. When the resulting
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
left behind by an interrupted build ever fails a build. The guard is
dependency-free (no `go.mod`/`go.sum` changes), carries the standard
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

Before the pipeline begins, go-toolchain runs a pre-flight check: if it is running under the Claude agent and its output is being hidden — piped into another command, redirected to a file, discarded to `/dev/null`, or captured via `$(...)` — it aborts immediately with an error (see the Claude output guard above; the guard is unconditional and has no opt-out). It then checks whether anything relevant changed since the last successful run: it fingerprints every `.go` file, `go.mod`/`go.sum`, and each file referenced by a `//go:embed` directive (enumerated for the main module via `go list`, so an edit to an embedded asset such as a static file or template is detected), and if the fingerprint matches and all `build/` outputs still exist it prints `⇒ Up to date, nothing to do` and exits without running the steps below. If `go list` cannot resolve the packages (e.g. the build is broken) it does not short-circuit. Otherwise the default workflow is:

1. Configures Go proxy and sumdb environment (via `GO_PROXY_CONFIG` or env vars)
2. Checks for outdated dependencies (auto-updates same-org deps)
3. Resolves vanity-URL module dependencies (injects replace directives for unreachable hosts)
4. Runs `go mod tidy`
5. Detects and runs `//go:generate` directives (if present)
6. Runs `go vet`: custom analyzers normalize assertions, migrate `gotest.tools`/fork-testify imports to upstream `stretchr/testify`, and insert explicit type conversions into cross-type `assert`/`require` `Equal`/`NotEqual` operands (resyncing the vendor tree afterward). Locally these fixes are applied in place; on CI (`CI` set) they run read-only and any change they would make is a hard failure instead — so importing the removed `wow-look-at-my/testify` fork fails CI rather than passing green
7. Checks for near-duplicate code blocks (warnings only)
8. Checks file lengths (warns at 500 lines, errors at 750)
9. Starts GOCACHEPROG server with local + web backends (if web cache credentials are configured). The local tier is a FUSE virtual filesystem backed by append-only pack files (see [docs/CACHE.md](docs/CACHE.md)); a cache hit returns a `DiskPath` into the mount and the kernel serves the body on demand from a pack, so no per-entry loose files or sidecars are written. Cache misses use the server's batch GET endpoint with prefetch — the server returns the requested entry plus temporally related entries from the same build, proactively populating the local cache. PUTs are LZ4-compressed and coalesced: instead of one HTTP PUT per object (a storm of thousands that saturates the cache server's admission control on a large build), buffered uploads are shipped as a single `/_batch/put` tar request — mirroring the batch GET coalescer — so a whole batch takes one server slot. The batch is retried as a whole on a `503` admission shed (honoring `Retry-After`), a per-object server error rolls back only that object's optimistic index claim, and the client falls back to individual PUTs against a cache server that does not support the batch endpoint. Each object is tagged with metadata describing what it is (sent as `X-Cache-Meta-*` headers on an individual PUT, or as per-entry manifest metadata in a batch):
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
14. If coverage meets the threshold, injects the cgroup→`GOMEMLIMIT` startup guard into each `main` package (unless `GO_TOOLCHAIN_AUTO_MEMLIMIT=off`), builds the project binaries into `build/`, then removes the transient guard files so they never linger in the working tree (the dirty-tree check ignores `gomemlimit_gen.go` in every git state)
15. Automatically adds `build/` to `.gitignore` (if in a git repo)
16. Runs benchmarks and compares against previously stored results
17. Submits a dependency snapshot to GitHub's Dependency Submission API (when `$CI` and `$GITHUB_REPOSITORY` are set), populating the repository's dependency graph with all direct and indirect Go module dependencies for vulnerability scanning and Dependabot alerts
18. Writes a GitHub Step Summary (when `$GITHUB_STEP_SUMMARY` is set) with a test case table, clickable source links, coverage stats, benchmark comparison, and a Gantt chart showing the pipeline timeline across all threads

## Development

```bash
# Run the tool on itself
go run ./src

# Run unit tests
go test ./src/...

# Run integration tests (requires bats, jq, attr)
bats tests/
```

## License

See repository for license details.
