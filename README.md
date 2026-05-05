# go-toolchain

A GitHub Action and CLI tool that builds Go projects with test coverage enforcement. Builds are gated on meeting a minimum coverage threshold, ensuring code quality doesn't regress.

## Features

- **Coverage enforcement** — fails the build if test coverage drops below 80%; emits a `::error::` workflow annotation in GitHub Actions so the failure is tagged in the run UI
- **Coverage watermarking** — optionally locks in a coverage floor using filesystem extended attributes, preventing regressions (with a 2.5% grace period)
- **Cross-compilation** — build for multiple OS/architecture combinations in parallel via the `matrix` subcommand
- **Benchmarks** — benchmarks run automatically after builds; compare against previous results stored in git notes
- **Near-duplicate detection** — scans Go source for structurally similar functions using AST comparison
- **File length checks** — warns at 500 lines, errors at 750 lines (with exemption support)
- **Auto-fix** — automatically fixes linter violations on non-CI systems
- **Go generate** — detects and runs `//go:generate` directives with hash-based approval
- **Dependency checking** — detects outdated dependencies and auto-updates same-org deps
- **Dependency graph submission** — automatically submits a dependency snapshot to GitHub's Dependency Submission API in CI, populating the repository's dependency graph for vulnerability alerts and Dependabot
- **Self-update** — update the binary in place via the `update` subcommand
- **CPU profiling** — run benchmarks with pprof profiling via the `profile` subcommand
- **Local install** — install the binary to `~/.local/bin` via the `install` subcommand
- **Coverage impact metrics** — each package/file/function shows how many percentage points it costs the total, making it easy to prioritize what to test next
- **Colorized output** — coverage percentages displayed with a red-to-green color gradient
- **CI summary** — automatically writes a rich GitHub Step Summary with test results, source links, coverage, benchmark comparisons, and a Mermaid Gantt chart of the pipeline timeline when running in GitHub Actions
- **Web-backed build cache** — GOCACHEPROG protocol server with local and web backends for shared build caching across CI runs (Go 1.24+). Uses server-side batch GET with prefetch: the server returns requested entries plus temporally related entries from the same build, proactively populating the local cache
- **Vanity URL resolution** — automatically detects and resolves vanity-URL module dependencies via Go proxy or go-import meta tags
- **Go proxy/sumdb support** — reads `GO_PROXY_CONFIG` (base64 JSON) to configure proxy URL, credentials (via ~/.netrc), and sumdb key automatically
- **Generated code exclusion** — automatically detects files with the standard `// Code generated ... DO NOT EDIT.` marker and excludes them from both test execution and coverage calculations (e.g. sqlc, protobuf, mockgen output)
- **Release management** — create GitHub releases with checksums, structured release notes, and rolling tag management via the `release` subcommand
- **npm package publishing** — CI automatically publishes cross-compiled binaries as scoped npm packages (one wrapper plus one per `(os, arch)`) to a Gitea npm registry; branch builds use prerelease versions with per-branch dist-tags

## GitHub Action Usage

Use the composite action in any `wow-look-at-my` org repo. Secrets are fetched automatically from [secret-server](https://github.com/wow-look-at-my/actions/tree/secret-server) via GitHub OIDC — no secret passing required:

```yaml
permissions:
  contents: write
  id-token: write
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
| `os`                | string   | `linux,darwin,windows` | Comma-separated target operating systems |
| `arch`              | string   | `amd64,arm64` | Comma-separated target architectures |
| `cgo`               | string   | `false`    | Enable CGO (disabled by default for static binaries) |
| `autorelease`       | string   | `false`    | Automatically create a GitHub release when on the default branch (requires `contents: write`) |
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
- **`update`** — self-update to the latest GitHub release
- **`release`** — create a GitHub release with checksums and structured release notes (`--tag`, `--from`, `--build`)
- **`version`** — show build version and staleness information
  - `raw` — print just the version number
  - `json` — print version info as JSON (version, commit, dates, staleness)

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
6. Runs `go vet` with auto-fix (on non-CI systems)
7. Checks for near-duplicate code blocks (warnings only)
8. Checks file lengths (warns at 500 lines, errors at 750)
9. Starts GOCACHEPROG server with local + web backends (if web cache credentials are configured). Cache misses use the server's batch GET endpoint with prefetch — the server returns the requested entry plus temporally related entries from the same build, proactively populating the local cache. PUTs upload entries individually with LZ4 compression. Each object is tagged with metadata headers describing what it is:
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
14. If coverage meets the threshold, builds the project binary into `build/`
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
