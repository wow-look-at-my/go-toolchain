# CLAUDE.md

## Build & Test

```bash
# Build and test (runs mod tidy, vet, tests with coverage, then builds)
go run ./src

# Run unit tests directly
go test ./src/...

# Run a single package's tests
go test ./src/cmd/
go test ./src/test/
go test ./src/build/

# Run integration tests (requires bats, jq, attr)
bats tests/

# Cross-compile
go run ./src matrix
```

## Project Structure

- `src/main.go` — entry point
- `src/cmd/` — CLI commands (root, matrix, benchmark, install) using Cobra
- `src/test/` — test runner, coverage parsing, watermark logic
- `src/build/` — build target resolution via `go list`
- `tests/` — BATS integration tests
- `action.yml` — GitHub Action definition

## Code Conventions

- Go module: `github.com/wow-look-at-my/go-toolchain`
- Go version: 1.24.7 (module), CI tests on 1.25
- CLI framework: `github.com/spf13/cobra`
- Test parsing: `gotest.tools/gotestsum/testjson`
- No Makefile — use `go run ./src` as the build entry point
- Binaries are output to `build/` directory
- Platform-specific files use `_linux.go`, `_darwin.go`, `_windows.go` suffixes (see `src/test/watermark_*.go`)
