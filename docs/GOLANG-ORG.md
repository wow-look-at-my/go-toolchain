# No contact with golang.org

go-toolchain, and the go command it embeds, and never open a connection to golang.org or to a host under it. No flag, environment variable or setting turns this off.

## The layers

| Layer | Where | What it does |
|---|---|---|
| go command web layer | gosmopolitan `cmd/go/internal/web` | Refuses a request, or a redirect, to any host under golang.org. |
| go command import paths | gosmopolitan `cmd/go/internal/vcs` | Maps `golang.org/x/NAME` to `github.com/golang/NAME` and each `google.golang.org` root to its GitHub repository, with no go-import request. An unknown path under either host is an error. |
| go command VCS | gosmopolitan `cmd/go/internal/modfetch/codehost` | Refuses a repository URL under golang.org before git runs, before and after git `insteadOf` rules. |
| go command defaults | gosmopolitan `cmd/go/internal/cfg`, `cmd/internal/telemetry` | The checksum database is off. Telemetry never uploads. |
| go-toolchain transport | `src/golangorg` | Wraps `http.DefaultTransport`, so a request from go-toolchain itself to golang.org fails before it connects. |
| go-toolchain environment | `src/goenv.go` | Sets `GOTOOLCHAIN=local` and `GOTELEMETRY=off`. |

## The google.golang.org roots

`golang.org/dl` maps to `github.com/golang/dl`. A module under another root fails to resolve. The error names the path. Add the root to `golangRepos` in the fork's `vcs.go` to support it.

## The tests

- `src/golangorg/golangorg_test.go` proves the host match, the setting parse and the transport refusal, a redirect included.
- `src/golangorg/scan_test.go` fails the build on any committed line outside a test. That names a golang.org endpoint, installs a `golang.org` module at a version, or sets `GOTOOLCHAIN` to `auto`.
- `src/goenv_test.go` proves that each setting is refused and that `GOTOOLCHAIN` and `GOTELEMETRY` are pinned.
