# go-toolchain

A GitHub Action and CLI that builds Go projects with test coverage enforcement. Builds are gated on a minimum coverage threshold, so coverage cannot regress.

## Features

- **Coverage enforcement** — the build fails below 80% coverage, and the failure is annotated in the GitHub Actions run UI.
- **Coverage watermarking** — optionally locks in a coverage floor (with a 2.5% grace period) so it can only go up.
- **Warnings budget** — more than 15 distinct warnings in a run fails the build, with a numbered recap. A repeated warning counts once. See [docs/WARNINGS-GATE.md](docs/WARNINGS-GATE.md).
- **One binary, every platform** — `matrix` builds a single fat APE that runs natively on Linux x64, macOS ARM64 and Windows x64; it's the org's only native output. See [docs/MATRIX.md](docs/MATRIX.md).
- **WebAssembly targets** — `wasm/js` and `wasm/wasip1`, opted into alongside (or instead of) the APE. See [docs/WASM.md](docs/WASM.md).
- **Benchmarks** — run automatically after builds, compared against previous results stored in git notes.
- **CLI test suites** — `*.dats` suites under `dats/` run against the freshly built binaries; a failure fails the build. See [docs/DATS-PHASE.md](docs/DATS-PHASE.md).
- **Near-duplicate detection** — finds structurally similar functions by comparing ASTs.
- **File length checks** — warns at 500 lines, fails at 750. Generated files are exempt unless `--count-generated` is passed.
- **Auto-fix, or CI check** — locally the linter fixes violations in place; on CI the same checks run read-only, and a non-canonical tree fails the build with a diff of the fix.
- **testify migration** — rewrites fork and `gotest.tools` imports to upstream `stretchr/testify`, adding the type conversions upstream's strict comparisons need. See [docs/VET.md](docs/VET.md).
- **Custom vet analyzers** — `mapset` and `sliceset` (a `map[K]bool` or a slice used as a set, rewritten in place to `go-containers/set`),
  `writeruns` (a document written one string at a time), `jsoninterp` (JSON built by formatting, concatenation or a template),
  and `commentnumbers` (a number in a comment, in digits or in words — a warning, so the warnings budget is what fails the build).
  See [docs/VET.md](docs/VET.md).
- **Go generate** — detects and runs `//go:generate` directives with hash-based approval.
- **Dependency handling** — auto-updates same-org deps; every `github.com/wow-look-at-my/` dependency tracks a branch via a `// go-toolchain:auto-branch` marker. See [docs/DEPS.md](docs/DEPS.md).
- **Dependency graph submission** — submits a dependency snapshot to GitHub in CI, feeding the repo's dependency graph. No opt-out; a failed submission fails the build.
- **Automatic GOMEMLIMIT** — every built binary caps its Go heap at the container's cgroup limit instead of being OOM-killed. See [docs/MEMLIMIT.md](docs/MEMLIMIT.md).
- **Output stall watchdog** — prints a loud `STALLED: no output for Ns` warning when the pipeline goes silent for 5+ seconds. Disable with `GO_TOOLCHAIN_NO_WATCHDOG=1`.
- **CPU profiling** — run benchmarks under pprof via the `profile` subcommand.
- **Local install** — `install` puts the binary in `~/.local/bin`.
- **Coverage impact metrics** — each package, file and function shows how many percentage points it costs the total, so it is obvious what to test next.
- **Colorized output** — coverage percentages on a red-to-green gradient.
- **CI summary** — writes a GitHub Step Summary with test results, source links, coverage, benchmark deltas and a Gantt chart of the pipeline.
- **One toolchain, one output shape** — every phase compiles with the gosmopolitan fork, and the only outputs are the fat APE and wasm. See [docs/MATRIX.md](docs/MATRIX.md).
- **Web-backed build cache** — a GOCACHEPROG server that shares a build cache across CI runs, with a FUSE-backed local tier. See [docs/CACHE.md](docs/CACHE.md).
- **Build profile** — per-action timings joined with cache outcomes: what the build spent its time on, and whether the cache helped. See [docs/PROFILE.md](docs/PROFILE.md).
- **Vanity URL resolution** — resolves vanity-URL module dependencies via the Go proxy or go-import meta tags.
- **Go proxy/sumdb support** — reads `GO_PROXY_CONFIG` (base64 JSON) for the proxy URL, credentials and sumdb key.
- **Generated code exclusion** — files carrying the standard `DO NOT EDIT.` marker are excluded from tests and coverage.
- **Release management** — `release` creates a GitHub release with checksums, structured notes and rolling tags.
- **Buildhost publishing** — CI publishes binaries to [buildhost](https://pazer.build) over OIDC, downloadable as raw binary, tar.gz, deb, Homebrew, npm or OCI.
- **Background update check** — a non-blocking check warns once when this binary is behind the latest published release. It never updates itself.
- **Build outputs only survive a green run** — `build/<target>` is deleted before the run, and again if it fails. See [docs/BUILD-OUTPUTS.md](docs/BUILD-OUTPUTS.md).
- **Agent output guard** — under an AI coding agent, go-toolchain refuses to run when its output is hidden by a pipe, redirect or capture. See [docs/AGENT-OUTPUT-GUARD.md](docs/AGENT-OUTPUT-GUARD.md).

## GitHub Action Usage

Use the composite action in any `wow-look-at-my` org repo. Secrets come from [secret-server](https://github.com/wow-look-at-my/actions/tree/secret-server) over OIDC, so nothing is passed in:

```yaml
permissions:
  contents: write          # dependency-graph submission
  id-token: write          # secret-server and buildhost autorelease
  security-events: write   # CodeQL SARIF upload
  actions: read            # the all-builds guard scans the run's jobs
  checks: read             # the same guard, the head commit's check runs
  deployments: write       # autorelease registers a GitHub Deployment
  artifact-metadata: write # autorelease posts an artifact storage record

jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: wow-look-at-my/go-toolchain@v1
```

The action fetches secrets, configures the Go proxy and private repo access, wires up the web build cache, runs `go-toolchain matrix`, and runs a CodeQL `security-and-quality` analysis around the build. Every permission above is required, and the build fails without it — [docs/ACTION.md](docs/ACTION.md) says what each one is for.

**dats suites on wow-linux.** dats always sandboxes. The action, on Linux and only when the tree has `dats/` suites, installs bubblewrap and probes it before the pipeline. It does not `sudo sysctl` (wow-linux's block is seccomp, not those knobs). If bwrap is blocked and docker is usable, dats falls back to docker; if neither, the job fails rather than skipping the suites. The action cannot change `runs-on`. A consumer with `dats/` on that fleet sets one line to the dind pool (`vars.CI_RUNNER_DIND`; the exact YAML is in [docs/ACTION.md](docs/ACTION.md)). Do not copy `wow-look-at-my/dats/action.yml` into the consumer workflow, and do not `uses: wow-look-at-my/dats` for this (that action downloads and runs the dats CLI; go-toolchain links `dats.Run` in-process). See [docs/DATS-PHASE.md](docs/DATS-PHASE.md).

**Autorelease permissions**: `autorelease` (on by default) publishes through buildhost's `buildhost-publish`, which registers a GitHub Deployment and posts an artifact storage record **as part of publishing** — neither has an opt-out and each fails the build without its grant, so a job that autoreleases must grant `deployments: write` and `artifact-metadata: write`. Job-level `permissions:` blocks REPLACE the workflow-level one, so a job that declares its own has to list these alongside everything else it needs. Without them the build runs to completion and then dies on `Resource not accessible by integration`.

**All-builds guard permissions**: since `no-all-builds-job#3` (2026-07-20) the guard scans the run's jobs (Actions API) and the head commit's check runs (Checks API) and **fails closed** when it cannot scan, so the workflow token must grant `actions: read` + `checks: read` as in the block above. Private repos hard-fail without them ("Resource not accessible by integration"); public repos happen to pass scope-less, but keep the block complete.

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
| `targets`           | string   | `''`       | Comma-separated wasm targets to add (`wasm/js`, `wasm/wasip1`), plus the special value `cosmo`. Empty (the default) builds the APE alone |
| `cosmo-platforms`   | string   | `linux/amd64,darwin/arm64,windows/amd64` | Platforms the one fat APE covers; `all` covers everything the fork can emit |
| `cgo`               | string   | `false`    | Enable CGO (off by default, for static binaries) |
| `autorelease`       | string   | `true`     | Publish `build/` to buildhost on every branch push (see [docs/ACTION.md](docs/ACTION.md)) |
| `autorelease_args`  | string   | `''`       | Extra publish options as `key=value` pairs; unknown keys fail the build |
| `allow-source-build` | string  | `false`    | Build go-toolchain from source when the buildhost binary is unavailable, instead of failing fast |
| `timeout`           | string   | `10`       | Timeout in minutes for the go-toolchain build step |
| `wait-ci`           | string   | `false`    | Wait for the latest go-toolchain CI run before downloading the release binary |
| `codeql`            | string   | `true`     | Run CodeQL `security-and-quality` analysis around the build |

### Build-output hand-off

Every run hands `build/` off to later jobs in the same workflow run. Downstream jobs download it without naming it:

```yaml
- uses: wow-look-at-my/actions@cache-download#latest
  with:
    path: dist   # no name: self-discovers this run's hand-off
```

A run that saves several hand-offs needs an explicit `name: go-build-<uploader job id>`. See [docs/ACTION.md](docs/ACTION.md).

## CLI Usage

```bash
# Install
go install github.com/wow-look-at-my/go-toolchain@latest

# Run tests and build (default workflow)
go-toolchain

# One fat APE covering Linux x64, macOS ARM64 and Windows x64 (the default)
go-toolchain matrix

# Pick the platforms the one binary covers
go-toolchain matrix --cosmo-platforms linux/amd64,linux/arm64

# WebAssembly builds (browser/Node.js and WASI) alongside the APE
go-toolchain matrix --targets wasm/js,wasm/wasip1,cosmo

# WebAssembly alone, no APE
go-toolchain matrix --targets wasm/js,wasm/wasip1

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

# Print the gosmopolitan release this host would build against
go-toolchain version cosmo

# Create a GitHub release with checksums
go-toolchain release --tag v1.0.0
```

### Flags

#### Persistent flags (shared across subcommands)

| Flag             | Default     | Description                                          |
|------------------|-------------|------------------------------------------------------|
| `--json`         | `false`     | Output coverage as JSON                              |
| `-v`, `--verbose` | `false`    | Verbose output: debug log level, plus per-test output lines |
| `--log-level`    | `info`      | Minimum log level: `debug`, `info`, `warn`, `error`, or `silent` |
| `--generate`     | `''`        | Run `go:generate` directives matching this hash      |
| `--threshold`    | `0.75`      | Similarity threshold for duplicate detection (0.0-1.0) |
| `--min-nodes`    | varies      | Minimum AST node count for duplicate detection       |
| `--cgo`          | `false`     | Enable CGO (disabled by default for static binaries) |
| `--count-generated` | `false`  | Count generated files in the file length check instead of skipping them |
| `--no-profile`   | `false`     | Skip the per-action build profile                    |

Debug output goes to stderr and info to stdout. Warnings and errors become `::warning`/`::error` annotations in GitHub Actions, and go to stderr everywhere else.

#### Root command flags

| Flag              | Default | Description                                                    |
|-------------------|---------|----------------------------------------------------------------|
| `--no-benchmark`  | `false` | Skip benchmarks after build                                    |
| `--benchtime`     | `''`    | Duration or count for each benchmark (e.g. `5s`, `1000x`)     |
| `-n`, `--count`   | `1`     | Number of times to run each benchmark                          |
| `--cpu`           | `''`    | GOMAXPROCS values to test with (comma-separated)               |

### Subcommands

- **`matrix`** — build the release APE, plus optional wasm targets (`--cosmo-platforms`, `--targets`, `--parallel`, `--no-benchmark`)
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

## OpenTelemetry trace export

Set `OTEL_EXPORTER_OTLP_ENDPOINT` and the pipeline's timings export as OTLP traces; leave it unset and nothing is exported, at no cost. See [docs/TRACING.md](docs/TRACING.md).

```bash
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 go-toolchain
```
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318 go-toolchain
```

## Documentation

- [docs/PIPELINE.md](docs/PIPELINE.md) — what the default workflow does, step by step
- [docs/MATRIX.md](docs/MATRIX.md) — the one-binary APE build, and per-platform cross-compilation
- [docs/WASM.md](docs/WASM.md) — the `wasm/js` and `wasm/wasip1` targets
- [docs/CACHE.md](docs/CACHE.md) — build cache architecture, pack format, remote tier
- [docs/PROFILE.md](docs/PROFILE.md) — the per-action build profile
- [docs/DEPS.md](docs/DEPS.md) — dependency updates and branch tracking
- [docs/VET.md](docs/VET.md) — the custom vet analyzers
- [docs/DATS-PHASE.md](docs/DATS-PHASE.md) — CLI test suites
- [docs/MEMLIMIT.md](docs/MEMLIMIT.md) — the injected GOMEMLIMIT guard
- [docs/BUILD-OUTPUTS.md](docs/BUILD-OUTPUTS.md) — when `build/` artifacts are deleted
- [docs/ACTION.md](docs/ACTION.md) — the composite GitHub Action
- [docs/CI.md](docs/CI.md) — this repo's own CI workflow
- [docs/TRACING.md](docs/TRACING.md) — OpenTelemetry trace export
- [docs/AGENT-OUTPUT-GUARD.md](docs/AGENT-OUTPUT-GUARD.md), [docs/WARNINGS-GATE.md](docs/WARNINGS-GATE.md), [docs/BUILDHOST-MANIFEST.md](docs/BUILDHOST-MANIFEST.md)

## Development

```bash
# Run the tool on itself
go run ./src

# Build and test (runs mod tidy, vet, tests with coverage, then builds)
go-toolchain
```

## License

See repository for license details.
