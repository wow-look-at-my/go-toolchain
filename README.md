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
- **Colorized output** — coverage percentages displayed with a red-to-green color gradient
- **CI summary** — automatically writes a rich GitHub Step Summary with test results, source links, coverage, benchmark comparisons, and a Mermaid Gantt chart of the pipeline timeline when running in GitHub Actions

## GitHub Action Usage

```yaml
- uses: wow-look-at-my/go-toolchain@latest
  with:
    json: 'false'             # output coverage report as JSON
    generate: ''              # run go:generate directives matching this hash
    working-directory: '.'    # working directory for the build
    binary: ''                # path to a pre-built go-toolchain binary
    os: 'linux,darwin,windows' # target OSes
    arch: 'amd64,arm64'       # target architectures
    cgo: 'false'              # enable CGO (disabled by default for static binaries)
    autorelease: 'false'      # create a GitHub release on the default branch
```

### Inputs

| Input               | Default    | Description                                              |
|---------------------|------------|----------------------------------------------------------|
| `json`              | `false`    | Output coverage report as JSON                           |
| `generate`          | `''`       | Run `go:generate` directives matching this hash          |
| `working-directory` | `.`        | Working directory for the build                          |
| `binary`            | `''`       | Path to a pre-built go-toolchain binary (skips release download) |
| `os`                | `linux,darwin,windows` | Comma-separated target operating systems |
| `arch`              | `amd64,arm64` | Comma-separated target architectures |
| `cgo`               | `false`    | Enable CGO (disabled by default for static binaries) |
| `autorelease`       | `false`    | Automatically create a GitHub release when on the default branch (requires `contents: write`) |

### Outputs

| Output     | Description                |
|------------|----------------------------|
| `coverage` | Total coverage percentage  |

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
- **`install`** — install the binary to `~/.local/bin`
- **`update`** — self-update to the latest GitHub release
- **`version`** — show build version and staleness information
- **`ignore`** — manage build-check exemptions
  - `coverage` — enable coverage ratchet (watermark)
  - `lines <file>` — exempt files from file-length checks
- **`unignore`** — remove build-check exemptions
  - `coverage` — remove coverage watermark
  - `lines <file>` — remove file-length exemptions

## How It Works

1. Checks for outdated dependencies (auto-updates same-org deps)
2. Runs `go mod tidy`
3. Detects and runs `//go:generate` directives (if present)
4. Runs `go vet` with auto-fix (on non-CI systems)
5. Checks for near-duplicate code blocks (warnings only)
6. Checks file lengths (warns at 500 lines, errors at 750)
7. Runs `go test` across all packages with coverage profiling
8. Parses coverage results and compares against the minimum threshold (80%, or watermark - 2.5%)
9. Reports cache size breakdown (Go build cache, toolchain downloads, module cache) when running in GitHub Actions
10. If coverage meets the threshold, builds the project binary into `build/`
11. Automatically adds `build/` to `.gitignore` (if in a git repo)
12. Runs benchmarks and compares against previously stored results
13. Writes a GitHub Step Summary (when `$GITHUB_STEP_SUMMARY` is set) with a test case table, clickable source links, coverage stats, benchmark comparison, and a Gantt chart showing the pipeline timeline across all threads

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
