# go-toolchain

A GitHub Action and CLI tool that builds Go projects with test coverage enforcement. Builds are gated on meeting a minimum coverage threshold, ensuring code quality doesn't regress.

## Features

- **Coverage enforcement** — fails the build if test coverage drops below 80%
- **Coverage watermarking** — optionally locks in a coverage floor using filesystem extended attributes, preventing regressions (with a 2.5% grace period)
- **Cross-compilation** — build for multiple OS/architecture combinations in parallel via the `matrix` subcommand
- **Benchmarks** — run Go benchmarks after build with the `--benchmark` flag
- **Local install** — install the binary to `~/.local/bin` via the `install` subcommand
- **Colorized output** — coverage percentages displayed with a red-to-green color gradient

## GitHub Action Usage

```yaml
- uses: wow-look-at-my/go-toolchain@latest
  with:
    cov-detail: ''            # 'func' or 'file' for detailed coverage
    json: 'false'             # output coverage report as JSON
    verbose: 'false'          # show test output line by line
    working-directory: '.'    # working directory for the build
```

### Inputs

| Input               | Default | Description                                         |
|---------------------|---------|-----------------------------------------------------|
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
go-toolchain

# Cross-compile for multiple platforms
go-toolchain matrix --os linux,darwin,windows --arch amd64,arm64

# Run benchmarks after build
go-toolchain --benchmark --benchtime 5s --count 3

# Install binary to ~/.local/bin
go-toolchain install
```

### Flags

| Flag                | Default                   | Description                                  |
|---------------------|---------------------------|----------------------------------------------|
| `--cov-detail`      | `''`                      | Detailed coverage: `func` or `file`          |
| `--json`            | `false`                   | Output coverage as JSON                      |
| `--verbose`         | `false`                   | Verbose test output                          |
| `--add-watermark`   | `false`                   | Set coverage watermark on project directory  |
| `--benchmark`       | `false`                   | Run benchmarks after build                   |
| `--benchtime`       | `''`                      | Duration or count for each benchmark (e.g. `5s`, `1000x`) |
| `-n`, `--count`     | `1`                       | Number of times to run each benchmark        |
| `--cpu`             | `''`                      | GOMAXPROCS values to test with (comma-separated) |

### Subcommands

- **`matrix`** — cross-compile for multiple platforms (`--os`, `--arch`, `--parallel`)
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
