# go-toolchain

A GitHub Action and CLI tool that builds Go projects with test coverage enforcement. Builds are gated on meeting a minimum coverage threshold, ensuring code quality doesn't regress.

## Features

- **Coverage enforcement** — fails the build if test coverage drops below 80%
- **Coverage watermarking** — optionally locks in a coverage floor using filesystem extended attributes, preventing regressions (with a 2.5% grace period)
- **Cross-compilation** — build for multiple OS/architecture combinations in parallel via the `matrix` subcommand
- **Benchmarks** — benchmarks run automatically after builds; compare against previous results stored in git notes
- **Near-duplicate detection** — scans Go source for structurally similar functions using AST comparison
- **File length checks** — warns at 500 lines, errors at 750 lines (with exemption support)
- **Auto-fix** — automatically fixes linter violations on non-CI systems
- **Go generate** — detects and runs `//go:generate` directives with hash-based approval
- **Dependency checking** — detects outdated dependencies and auto-updates same-org deps
- **Self-update** — update the binary in place via the `update` subcommand
- **CPU profiling** — run benchmarks with pprof profiling via the `profile` subcommand
- **Local install** — install the binary to `~/.local/bin` via the `install` subcommand
- **Coverage impact metrics** — each package/file/function shows how many percentage points it costs the total, making it easy to prioritize what to test next
- **Colorized output** — coverage percentages displayed with a red-to-green color gradient
- **CI summary** — automatically writes a rich GitHub Step Summary with test results, source links, coverage, benchmark comparisons, and a Mermaid Gantt chart of the pipeline timeline when running in GitHub Actions
- **S3-backed build cache** — GOCACHEPROG protocol server with local and S3 backends for shared build caching across CI runs (Go 1.24+)
- **Vanity URL resolution** — automatically detects and resolves vanity-URL module dependencies via Go proxy or go-import meta tags
- **Go proxy/sumdb support** — configures pazer.io proxy and sumdb endpoints with automatic environment variable normalization
- **Release management** — create GitHub releases with checksums, structured release notes, and rolling tag management via the `release` subcommand

## GitHub Action Usage

Use the composite action in any `wow-look-at-my` org repo. Secrets are fetched automatically from [secret-server](https://github.com/wow-look-at-my/secret-server) via GitHub OIDC — no secret passing required:

```yaml
permissions:
  contents: write
  id-token: write

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: wow-look-at-my/go-toolchain@v1
```

The action handles everything: fetching secrets, configuring the Go proxy, private repo access, S3 build cache, and running `go-toolchain matrix`.

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
| `timeout`           | string   | `10`       | Timeout in minutes for the go-toolchain build step |

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

# Run benchmarks with CPU profiling
go-toolchain profile --web

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

# Enable coverage watermark
go-toolchain ignore coverage

# Exempt a file from length checks
go-toolchain ignore lines path/to/long_file.go
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
| `--profile`      | `''`        | Self-profile go-toolchain (`trace`, `cpu:path`, `mem:path`, `trace:path`) |

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
- **`profile`** — run benchmarks with pprof profiling (`--type cpu|mem|mutex|block`, `--web`, `--no-pprof`, `-o`)
  - `open [file]` — open a pprof/trace file in the browser (defaults to latest CPU profile)
- **`install`** — install the binary to `~/.local/bin`
- **`update`** — self-update to the latest GitHub release
- **`release`** — create a GitHub release with checksums and structured release notes (`--tag`, `--from`, `--build`)
- **`version`** — show build version and staleness information
  - `raw` — print just the version number
  - `json` — print version info as JSON (version, commit, dates, staleness)
- **`ignore`** — manage build-check exemptions
  - `coverage` — enable coverage ratchet (watermark)
  - `lines <file>` — exempt files from file-length checks
- **`unignore`** — remove build-check exemptions
  - `coverage` — remove coverage watermark
  - `lines <file>` — remove file-length exemptions

## Self-Profiling

go-toolchain automatically captures CPU and memory profiles of itself on every run. Profiles are written to `/tmp/go-toolchain-profile/`, overwriting the previous run:

- `cpu.pprof` — CPU profile (flamegraph via `go tool pprof`)
- `mem.pprof` — heap snapshot at exit

Execution tracing is opt-in (higher overhead):

```bash
# Enable execution trace (goroutine scheduling, blocking, GC)
go-toolchain --profile trace

# Custom output path
go-toolchain --profile trace:custom/trace.out

# Override default CPU/mem paths
go-toolchain --profile cpu:my_cpu.pprof --profile mem:my_mem.pprof
```

Open profiles with the built-in viewer:

```bash
# Open the latest CPU profile in the browser
go-toolchain profile open

# Open a specific file
go-toolchain profile open /tmp/go-toolchain-profile/trace.out
```

## How It Works

1. Configures Go proxy and sumdb environment (pazer.io support)
2. Checks for outdated dependencies (auto-updates same-org deps)
3. Resolves vanity-URL module dependencies (injects replace directives for unreachable hosts)
4. Runs `go mod tidy`
5. Detects and runs `//go:generate` directives (if present)
6. Runs `go vet` with auto-fix (on non-CI systems)
7. Checks for near-duplicate code blocks (warnings only)
8. Checks file lengths (warns at 500 lines, errors at 750)
9. Starts GOCACHEPROG server with local + S3 backends (if S3 credentials are configured). Each S3 object is tagged with metadata headers describing what it is:
   - `Object-Type` — file type detected from magic bytes (`go-archive`, `elf-binary`, `macho-binary`, `pe-binary`, `go-object`, or `unknown`)
   - `Go-Version` — the Go compiler version that produced the artifact (e.g. `go1.24.7`), extracted from Go archive headers
   - `Target` — the target platform (e.g. `linux/amd64`), extracted from Go archive headers
   - `Body-Size` — original uncompressed size in bytes
   - `Compression` — compression algorithm (`lz4`)
   - `Toolchain-Version` — the go-toolchain version that cached the entry
   - `Created` — RFC 3339 timestamp of when the entry was first cached
10. Runs `go test` across all packages with coverage profiling
11. Parses coverage results, displays per-item impact on total coverage, and compares against the minimum threshold (80%, or watermark - 2.5%)
12. Reports cache size breakdown (Go build cache, toolchain downloads, module cache) when running in GitHub Actions
13. If coverage meets the threshold, builds the project binary into `build/`
14. Automatically adds `build/` to `.gitignore` (if in a git repo)
15. Runs benchmarks and compares against previously stored results
16. Writes a GitHub Step Summary (when `$GITHUB_STEP_SUMMARY` is set) with a test case table, clickable source links, coverage stats, benchmark comparison, and a Gantt chart showing the pipeline timeline across all threads

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
