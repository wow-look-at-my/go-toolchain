# No contact with golang.org

go-toolchain, and the go command it embeds, and never open a connection to golang.org or to a host under it. No flag, environment variable or setting turns this off.

## The layers

| Layer | Where | What it does |
|---|---|---|
| go command web layer | gosmopolitan `cmd/go/internal/web` | Refuses a request, or a redirect, to any host under golang.org. |
| go command import paths | gosmopolitan `cmd/go/internal/vcs` | Maps `golang.org/x/NAME` to `github.com/golang/NAME` and each `google.golang.org` root to its GitHub repository, with no go-import request. An unknown path under either host is an error. |
| go command VCS | gosmopolitan `cmd/go/internal/modfetch/codehost` | Refuses a repository URL under golang.org before git runs, before and after git `insteadOf` rules. |
| go command defaults | gosmopolitan `cmd/go/internal/cfg`, `cmd/internal/telemetry` | The checksum database is off. Telemetry never uploads. `GOPROXY`, `GOSUMDB` and `GOTOOLCHAIN` are not read. |
| go-toolchain transport | `src/golangorg` | Wraps `http.DefaultTransport`, so a request from go-toolchain itself to golang.org fails before it connects. |
| go-toolchain environment | `src/goenv.go` | Sets `GOTOOLCHAIN=local` and `GOTELEMETRY=off`. A `GOPROXY`, `GOSUMDB` or `GOVULNDB` that names golang.org ends the run with an error that names the setting. |
| CI and the action | `action.yml`, `.github/workflows/ci.yml`, `.github/scripts/build-from-github.sh` | Builds each generator from a GitHub checkout whose `golang.org/x` and `google.golang.org` requirements point at GitHub. |

## The google.golang.org roots

| Root | GitHub repository |
|---|---|
| `google.golang.org/api` | `github.com/googleapis/google-api-go-client` |
| `google.golang.org/appengine` | `github.com/golang/appengine` |
| `google.golang.org/cloud` | `github.com/googleapis/google-cloud-go` |
| `google.golang.org/genai` | `github.com/googleapis/go-genai` |
| `google.golang.org/genproto` | `github.com/googleapis/go-genproto` |
| `google.golang.org/grpc` | `github.com/grpc/grpc-go` |
| `google.golang.org/protobuf` | `github.com/protocolbuffers/protobuf-go` |

`golang.org/dl` maps to `github.com/golang/dl`. A module under another root fails to resolve. The error names the path. Add the root to `golangRepos` in the fork's `vcs.go` to support it.

## The tests

- `src/golangorg/golangorg_test.go` proves the host match, the setting parse and the transport refusal, a redirect included.
- `src/golangorg/scan_test.go` fails the build on any committed line outside a test. That names a golang.org endpoint, installs a `golang.org` module at a version, or sets `GOTOOLCHAIN` to `auto`.
- `src/goenv_test.go` proves that each setting is refused and that `GOTOOLCHAIN` and `GOTELEMETRY` are pinned.
- The fork's `web`, `vcs`, `codehost` and `modfetch` tests prove the go command side.
