# go-toolchain

A GitHub Action and CLI tool that builds Go projects with test coverage enforcement. Builds are gated on meeting a minimum coverage threshold, ensuring code quality doesn't regress.

## Features

- **Coverage enforcement** — fails the build if test coverage drops below a configurable minimum (default 80%)
- **Coverage watermarking** — optionally locks in a coverage floor using filesystem extended attributes, preventing regressions (with a 2.5% grace period)
- **Cross-compilation** — build for multiple OS/architecture combinations in parallel via the `matrix` subcommand
- **Benchmarks** — run Go benchmarks with configurable options via the `benchmark` subcommand
- **Local install** — install the binary to `~/.local/bin` via the `install` subcommand
- **Colorized output** — coverage percentages displayed with a red-to-green color gradient

## GitHub Action Usage

```yaml
- uses: wow-look-at-my/go-toolchain@latest
  with:
    min-coverage: '80'        # minimum coverage % (0 = test only, no build)
    cov-detail: ''            # 'func' or 'file' for detailed coverage
    json: 'false'             # output coverage report as JSON
    verbose: 'false'          # show test output line by line
    working-directory: '.'    # working directory for the build
```

### Inputs

| Input               | Default | Description                                         |
|---------------------|---------|-----------------------------------------------------|
| `min-coverage`      | `80`    | Minimum coverage percentage (0 = test only)         |
| `cov-detail`        | `''`    | Show detailed coverage: `func` or `file`            |
| `json`              | `false` | Output coverage report as JSON                      |
| `verbose`           | `false` | Show test output line by line                       |
| `working-directory` | `.`     | Working directory for the build                     |

### Outputs

| Output     | Description                |
|------------|----------------------------|
| `coverage` | Total coverage percentage  |

## CLI Usage

```bash
# Install
go install github.com/wow-look-at-my/go-toolchain/src@latest

# Run tests and build (default workflow)
go-toolchain --min-coverage 80

# Test only, no build
go-toolchain --min-coverage 0

# Cross-compile for multiple platforms
go-toolchain matrix --os linux,darwin,windows --arch amd64,arm64

# Run benchmarks
go-toolchain benchmark --benchtime 5s --count 3 --benchmem

# Install binary to ~/.local/bin
go-toolchain install
```

### Flags

| Flag                | Default                   | Description                                  |
|---------------------|---------------------------|----------------------------------------------|
| `--min-coverage`    | `80`                      | Minimum coverage percentage (0 = test only)  |
| `--cov-detail`      | `''`                      | Detailed coverage: `func` or `file`          |
| `--json`            | `false`                   | Output coverage as JSON                      |
| `--verbose`         | `false`                   | Verbose test output                          |
| `--add-watermark`   | `false`                   | Set coverage watermark on `go.mod`           |

### Subcommands

- **`matrix`** — cross-compile for multiple platforms (`--os`, `--arch`, `--parallel`)
- **`benchmark`** — run benchmarks (`--benchtime`, `--count`, `--cpu`, `--benchmem`)
- **`install`** — install the binary to `~/.local/bin`

## How It Works

1. Runs `go mod tidy` and `go vet`
2. Runs `go test` across all packages with coverage profiling
3. Parses coverage results and compares against the minimum threshold
4. If coverage meets the threshold, builds the project binary into `build/`
5. Optionally enforces a coverage watermark — once set, coverage can't drop more than 2.5% below the recorded high

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
