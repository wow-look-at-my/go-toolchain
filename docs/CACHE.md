# Build caching

go-toolchain ships no cache server of its own. The gosmopolitan fork's
`cmd/go` links `github.com/wow-look-at-my/go-s3-server/cacheclient` in
process and consults it directly, ahead of `GOCACHEPROG` — see that repo's
CLAUDE.md.

## What this repo still does

Nothing. The CI cache-config check moved into gosmopolitan itself
(`cmd/go/internal/cache`'s `validateCIShared`, called from `initDefaultCache`)
— it now fails outright, with no downgrade-to-warning knob, whenever CI is set
and `GO_BUILDCACHE_CONFIG` is not. `GO_BUILDCACHE_CONFIG` (base64 JSON:
`endpoint`, `bucket`, `username`, `password` — the deprecated S3-style
`key_id`/`access_key`/`region` spellings still parse, with a warning) reaches
gosmopolitan's `cmd/go` through plain environment inheritance, since every
`go build`/`go test` invocation is a child process of this binary.

## History

Until 2026-08, this repo ran its own `GOCACHEPROG` protocol server
(`src/cache/`, `src/cmd/cacheprog.go`). A local disk/FUSE-pack tier, a
`go-s3-server/cacheclient`-backed remote tier, a shared daemon so sibling `go`
invocations skipped reloading the web index.

Gosmopolitan grew the same remote client directly into `cmd/go`, and its
`chooseCache` now picks that shared tier over `GOCACHEPROG` whenever
`GO_BUILDCACHE_CONFIG` is configured. That made
this repo's own server dead weight on every build: it still paid real setup
cost (a stats listener, a daemon loading the remote web index) for a path no
child `go` process.

The functionality is not gone, only the duplicate copy of it. Local disk
caching, the remote tier, and its integrity guards (checksum, build-id,
module-index) all still run, inside gosmopolitan's. What is genuinely
lost. Gosmopolitan's `cmd/go` prints nothing
about the shared cache on success (only `go: shared build cache disabled:
<err>` on failure, to stderr, which passes through this binary's own output
unchanged) and exposes no summary via `go env` or `-debug-actiongraph`.
Real hit/miss/put counts, and the poison-tripwire counters
(`miss_checksum`/`miss_buildid`/`miss_modindex`), do still exist — as
`cacheclient.WebBackend.SummarySnapshot()` inside gosmopolitan's own process. Neither is
observable from a generic `go-toolchain` CI job without a change to
gosmopolitan itself (to print or otherwise expose the summary) — that is a
real, open gap.

`src/profile/`'s build profile now reports only actiongraph timing (wall
time per action, the slowest actions) — no cache fields.
