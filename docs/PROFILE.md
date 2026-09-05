# Per-action build profile

Every run profiles what the build actually did, per compiler/linker/test action. go-toolchain injects `-debug-actiongraph=<file>` into each `go build` / `go test` invocation (one dump per invocation. Matrix targets each get their own), then merges the dumped action. It carries no cache hit/miss data: build caching now lives in gosmopolitan's `cmd/go`, out of this process's view — see [CACHE.md](CACHE.md).

The result is emitted four ways at the end of the run:

- **Console section** (always on, compact):

  ```
  ⇒ Build profile: 1564 actions (1564 executed), 98% cache-satisfied (hit-local 808  hit-remote 0  miss 15)
     Slowest actions:
        7.90s  test run  github.com/wow-look-at-my/go-toolchain/src/cmd       -
        6.83s  test run  github.com/wow-look-at-my/go-toolchain/src/vet       -
        938ms  link      github.com/wow-look-at-my/go-toolchain/src.test      miss
     Rebuilt despite cache (miss+put):
        1.82s  github.com/wow-look-at-my/go-toolchain/src/cache
  ```

  The outcome column is the cache verdict for that action (`hit-local`, `hit-remote`, `miss`, with `+put` when the output was stored this run. `-` means no cache get was observed, e.g. test-run actions). The "Rebuilt despite cache" list aggregates miss+put wall time by package — on a warm build these are the cache defeats worth investigating.

- **`build/profile.json`** (plus a copy at `$TMPDIR/go-toolchain-profile/profile.json`): the full machine-readable join. Top-level fields: `schema` (currently 1), `created`, `total_actions` / `executed_actions`, `cache_outcomes` (row count per outcome, `unknown` = no get observed), `cache_satisfied_pct` (hits / (hits+misses) over known outcomes), `wall_ms_total` (sum of per-action wall time — total work, not elapsed time), `cache` (the run-wide hit/put/miss counters), `web` (the remote tier's diagnostic counters — including the poison tripwires `miss_checksum` / `miss_buildid` / `miss_modindex` — plus `index_keys` / `index_authoritative` from the startup index fetch), and `actions`. One row per merged graph action (`action_id`, `package`, `mode`, `wall_ms`, `cmd_real_ms`/`cmd_user_ms`/`cmd_sys_ms`, `outcome`, `put`, `bytes`, `get_us`/`put_us`, `start`), sorted by wall time descending. CI asserts on this file (see `.github/workflows/ci.yml`).

- **Chrome trace lanes**: each executed action becomes a timed event in `$TMPDIR/go-toolchain-profile/trace.json` on a `go actions #NN` lane (a greedy interval scheduler keeps parallel actions side-by-side), with `package`/`mode`/`action_id`/`cache` args.

- **GitHub Step Summary**: a profile table (cache totals + top slowest actions) next to the existing pipeline Gantt.

Actiongraph collection and the report are skipped with `--no-profile`, and skip cleanly on paths that never reach `go build`/`go test`. Parsing is defensive: a missing or malformed dump is skipped (with a warning) and can never fail the build.

