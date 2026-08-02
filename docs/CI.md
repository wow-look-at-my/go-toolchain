# This repo's own CI workflow

`.github/workflows/ci.yml` — five stages: `host-build` → `build` → three
`smoke-*` jobs → `publish`. It dogfoods the composite action and gates the
release on the artifacts actually running.

## host-build

Builds go-toolchain from source into a host-native binary. Its cache-validation
step execs `build/go-toolchain` directly, which is safe only because this job
never builds the APE — an APE rewrites its own header on first exec.

**Build-log duration regression guard.** The "Build and test" step
(`go run ./src`) tees its output to `$RUNNER_TEMP/build-log.txt` (`set -euo
pipefail` — GHA's default `bash -e {0}` has no `pipefail`, so a plain `cmd |
tee file` would let a real build/test failure hide behind `tee`'s own exit
code). This job's `cache: false` setup-go guarantees a cold module cache, so
`go mod tidy -v`'s verbose `go: downloading X` / `go: found X` lines always
fire — exactly the output where a real bug shipped once: a wrapper stamped
even sub-second lines with a duration (`go: downloading X 0.00s`), because it
forgot to gate on a minimum elapsed time (see `src/logx/logx.go`'s
`minDurationToShow` and `src/cmd/console.go`'s `timedLineWriter` /
`timedLineMinDuration`, both fixed to require >= 1s before stamping).

Two follow-up steps check the capture. First, `ansifilter` (not this repo's
own `ansiRE`, so a bug in that regex can't also blind the test verifying it)
strips ANSI color codes — but only after a sanity check that raw ANSI codes
were actually present, guarding against the tee pipe (or some future isatty
gate) silently breaking color output and making the duration check pass for
the wrong reason. Then a TypeScript step asserts no `go: `-prefixed line (cmd/go's
own messages, which never carry a duration themselves — any stamp there was
added by us) carries a duration under 1s, and separately asserts at least one
`go: downloading` line was captured at all, so the check can't silently pass
by verifying nothing. It is deliberately scoped to `go: ` lines rather than
"any duration under 1s anywhere in the log": go-toolchain's own named
step/test timers (e.g. `vet: gofmt 0.17s`) are intentionally unconditional — a
named operation's own time is always worth reporting — and must not be
flagged.

## build

Runs the composite action (`uses: ./`) with
`targets: cosmo,darwin/amd64,darwin/arm64,windows/arm64` — the fat APE plus the
three native carve-outs — and `autorelease: false`. The cosmo bootstrap
downloads the gosmopolitan toolchain from its default `?branch=master` and
cold-compiles its stdlib, hence the raised `timeout: 15`.

## The three smoke jobs

Each is `timeout-minutes`-bounded and downloads the `go-build-build` hand-off
the `build` job uploaded, via `wow-look-at-my/actions@cache-download#latest`
(run-keyed cross-OS cache wrapper; the download `path` is the destination
directory). The action names its hand-off `go-build-<job id>` per calling job,
with a `.m<job-index>` suffix per leg when the caller is a matrix job, so
concurrent same-run saves never collide on one key.

They EXECUTE throwaway copies of the artifacts in `dist/`, never the downloaded
file itself.

**linux** — APE magic `MZqFpD` on the linux/amd64 slot, then `version`,
`--help`, and the FULL default pipeline in a tiny module under the APE. Plus an
agent-output-guard assertion on a fresh throwaway APE copy: per-command
`CLAUDECODE=1` — never job-wide, the other smoke steps must stay inert — with
stdout to `/dev/null` must exit 1 with the "refused to run" message, and
`version` must stay exempt.

**macOS** — asserts the darwin/arm64 slot is native Mach-O (`cffaedfe`) and runs
the FULL pipeline with that native binary; this is the consumer-critical mac
gate. The APE gate on this host is REDUCED (magic on a linux slot + `version` +
`--help` inside a module = official-Go bootstrap under the APE loader) because
the pipeline WEDGES AT EXIT under the APE on macOS. Root-caused from SIGQUIT
dumps (run 28742069477; issue #276) to the fork running unix-socket fds
blocking/netpoller-less on darwin hosts, so the cache daemon's `Listener.Close`
deadlocks. All pipeline PHASES are green under the APE now that
`cacheProgCommand` wraps the GOCACHEPROG self-exec in a sh script.

**Windows** — `version` and `--help` only: gobootstrap downloads
`go<version>.<os>-<arch>.tar.gz`, and go.dev serves no windows variant (windows
archives are `.zip`).

> **Owner-ruled smoke contract (Windows).** NO workflow-side Go provisioning —
> no `setup-go`, that bypasses the bootstrap requirement — and no help-flag
> `needsGo` carve-outs. `--help`'s bootstrap must resolve the runner image's
> EXISTING Go through the APE's OWN NT-side `exec.LookPath`. Broken
> pre-gosmopolitan#63 (unix-style `:` PATH walk with no `.exe` suffixing on NT
> hosts), fixed in fork v237+. If the image ever drops Go, the red is honest —
> escalate to the owner.

## publish

The single publish path, gated on all three smokes. It downloads the same
`go-build-build` hand-off into `build/`, then `wow-look-at-my/buildhost`'s
buildhost-publish action publishes it via its `path` input and OIDC — no
checkout, no artifacts API. A trailing `if: always()`
`wow-look-at-my/actions@cache-cleanup#latest` step, backed by the job's
`actions: write`, deletes the run's `cache-xfer-*` hand-off entries and
age-sweeps 12h-old leftovers.

The job's permissions are load-bearing (`ci.yml:498-503`):

```yaml
permissions:
  id-token: write
  contents: read
  actions: write            # the cache-cleanup step
  deployments: write        # the publish registers a GitHub Deployment
  artifact-metadata: write  # the publish posts an artifact storage record
```

`deployments: write` and `artifact-metadata: write` are what let buildhost-publish
register the Deployment and post the storage record. Both are mandatory with no
opt-out — see `docs/ACTION.md`.

## No GitHub Actions artifacts anywhere

Job hand-offs ride `wow-look-at-my/actions@cache-upload#latest` /
`@cache-download#latest` — run-keyed GitHub cache entries whose exact key
includes `run_attempt`, with downloads falling back to the run's previous
attempt and absence failing loud. A single-file upload (the `host-build`→`build`
`host-go-toolchain` hand-off of `build/go-toolchain`) is stored raw and restored
basename+exec-bit into the destination directory, where the action's `binary`
input consumes it. The old debug-only `build-profiles` artifact is gone; the
profile's home is the Step Summary table.

## Tidy self-heals against cache-served module-index damage

`src/cmd/modindexretry.go`'s `runModTidy` detects cmd/go's `corrupt index`
failure — a damaged or mis-keyed module-index cache entry passes every content
gate the cacheprog can apply, having no build id and an opaque action key —
disables the Go module index for the remainder of the run (`GODEBUG=goindex=0`),
and retries once.

---

*Provenance: merged from three near-duplicate `ci.yml` bullets that had
accumulated in CLAUDE.md — three generations of one bullet, not three topics.
Where they disagreed, the source decided: the newest carried the `.m<job-index>`
matrix suffix (kept) but had DROPPED the publish job's `deployments: write` /
`artifact-metadata: write` clause, which `ci.yml:502-503` still grants and
`action.yml:43` still requires (restored). The oldest predated the owner-ruled
Windows smoke contract entirely.*
