# CLAUDE.md

## Build & Test

**Important:** `go build`, `go test`, etc. are often blocked in this environment because the Go toolchain version in go.mod may be newer than what's locally installed. The go-toolchain binary handles bootstrapping the correct Go version automatically.

**Always use the released `go-toolchain` binary** to build and test. If it's not already installed, download it from GitHub releases:

```bash
# Download and install go-toolchain (do this first if not installed)
curl -sL "https://github.com/wow-look-at-my/go-toolchain/releases/latest/download/go-toolchain_linux_amd64" -o /tmp/go-toolchain
chmod +x /tmp/go-toolchain
cp /tmp/go-toolchain /usr/local/bin/go-toolchain

# Build and test (runs mod tidy, vet, tests with coverage, then builds)
go-toolchain

# Cross-compile
go-toolchain matrix

# Run integration tests (requires bats, jq, attr)
bats tests/
```

Do NOT use `go run ./src`, `go build`, `go test`, `go vet`, or any bare `go` commands directly — they will fail if the local Go version doesn't match go.mod.

## Coverage Analysis

After running `go-toolchain`, the output includes a "Coverage targets" section showing the top functions to test, ranked by potential gain (how much total coverage would increase if the function were fully covered). Functions are split into two groups:

- **UNTESTED** (0% covered) — likely just needs one test that calls the function
- **PARTIAL** (some coverage) — needs specific inputs to hit uncovered branches

Each line shows: `+gain%  N stmts  file:line  FunctionName` (stmts = uncovered Go statements). Always start from the top of the list when improving coverage.

## Project Structure

- `src/main.go` — entry point
- `src/cmd/` — CLI commands (root, matrix, bench, lint, install, update, version, release, ignore/unignore, cacheprog) using Cobra; also includes dependabot (automatic dependency graph submission to GitHub in CI)
- `src/test/` — test runner, coverage parsing, watermark logic
- `src/build/` — build target resolution via filesystem walking
- `src/gomod/` — shared Go module utilities (module path reading, main package discovery)
- `src/cache/` — GOCACHEPROG protocol server with local and web backends, server-side batch GET with prefetch
- `src/trace/` — OpenTelemetry trace export for build pipeline timings
- `src/vet/` — custom vet checks (assert normalization, unused imports)
- `tests/` — BATS integration tests
- `action.yml` — composite GitHub Action (fetches secrets via OIDC, builds with go-toolchain, optionally uploads build artifacts)
- `.github/workflows/ci.yml` — CI workflow (builds from source, tests the action, releases)

## Code Conventions

- Go module: `github.com/wow-look-at-my/go-toolchain`
- Go version: 1.24.7 (module), CI tests on 1.25
- CLI framework: `github.com/spf13/cobra`
- Test parsing: `gotest.tools/gotestsum/testjson`
- No Makefile — use `go run ./src` as the build entry point
- Binaries are output to `build/` directory
- Platform-specific files use `_linux.go`, `_darwin.go`, `_windows.go` suffixes (see `src/test/watermark_*.go`)

## Documentation

- **Always keep `README.md` up to date** when adding new features, flags, subcommands, or changing existing behavior. The README is the primary user-facing documentation and must accurately reflect the current state of the CLI and GitHub Action.
- When adding a new subcommand, add it to the Subcommands section and include a CLI usage example.
- When adding a new flag, add it to the appropriate flags table (persistent or command-specific).
- When changing action.yml inputs, update the Action Usage section accordingly.
- When changing the build pipeline steps (e.g. adding a new check or phase), update the "How It Works" section.

## Known Issues

- **`TestStatsStreaming` (src/cache/cache_test.go)**: This test has been observed to fail intermittently (got `Puts=0` instead of `1` at line 499). The failure was not reproducible in isolation — 50 iterations with `-race` all passed. Root cause is **undiagnosed**. Likely a race condition in stats delivery over the unix socket, possibly triggered only under heavy parallel test load. Do not dismiss this as "flaky" — investigate properly if it fails again.
