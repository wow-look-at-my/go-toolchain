# This repo's own CI workflow

`.github/workflows/ci.yml` — five stages: `host-build` → `build` → three
`smoke-*` jobs → `publish`. It dogfoods the composite action and gates the
release on the artifacts actually running.

## host-build

Builds go-toolchain from source into a host-native binary. Its cache-validation
step execs `build/go-toolchain` directly, which is safe only because this job
never builds the APE — an APE rewrites its own header on first exec.

## build

Runs the composite action (`uses: ./`) with NO target inputs and
`autorelease: false`, so it exercises the exact default a consumer gets: ONE fat
APE (`go-toolchain_cosmo_fat`) covering linux/amd64, darwin/arm64 and
windows/amd64, plus `buildhost-artifacts.json`. The cosmo bootstrap downloads
the gosmopolitan toolchain from its default `?branch=master` and cold-compiles
its stdlib, hence the raised `timeout: 15`.

A trailing step asserts the shape the default exists to produce: the manifest is
schema 1 with exactly one artifact, its platform set is the three above, its
download filename is the plain `go-toolchain`, and `build/` contains NO file
matching the per-platform grammar. That last check is the one that stays honest
over time — a stray `<name>_<os>_<arch>` file would silently restore the
N-downloads-of-one-binary shape without failing anything else.

## The three smoke jobs

Each is `timeout-minutes`-bounded and downloads the `go-build-build` hand-off
the `build` job uploaded, via `wow-look-at-my/actions@cache-download#latest`
(run-keyed cross-OS cache wrapper; the download `path` is the destination
directory). The action names its hand-off `go-build-<job id>` per calling job,
with a `.m<job-index>` suffix per leg when the caller is a matrix job, so
concurrent same-run saves never collide on one key.

They EXECUTE throwaway copies of the artifacts in `dist/`, never the downloaded
file itself.

All three run the SAME file, `dist/go-toolchain_cosmo_fat` — there is one
artifact now, and each job proves it boots on that host.

**linux** — APE magic `MZqFpD`, then `version`, `--help`, and the FULL default
pipeline in a tiny module under the APE. The agent-output-guard regression is a
committed dats fixture
(`.github/dats-fixtures/smoke-linux-agent-output-guard.dats`), copied into that
module's `dats/` dir and run automatically by the pipeline's dats phase — not
hand-rolled bash, so it exercises the real released APE the same way a
consumer's own build would.

**macOS** — magic, `version`, and the FULL default pipeline under the APE, plus
the darwin sibling of the guard fixture
(`smoke-macos-agent-output-guard.dats`). This is the consumer-critical mac gate,
and it is deliberately not reduced.

It used to be: darwin/arm64 shipped as a native carve-out and the mac gate ran
that binary, because a full pipeline WEDGED AT EXIT under the APE on macOS —
root-caused from SIGQUIT dumps (run 28742069477; issue #276) to the fork running
unix-socket fds blocking and netpoller-less on darwin hosts, so the cache
daemon's `Listener.Close` deadlocked against its own blocked `accept4`, which a
close never wakes on XNU. Every pipeline PHASE went green once
`cacheProgCommand` wrapped the GOCACHEPROG self-exec in a sh script; only the
exit path remained. The fork's darwin netpoller is a kqueue port now, so that
deadlock should be gone. Running the full pipeline here is how we find out: a
red is the honest answer that it is not, and the job's `timeout-minutes` bounds
the hang.

Note which guard implementation the mac fixture now exercises. The APE reports
`runtime.GOOS == "cosmo"`, so it compiles the `_cosmo` sockpeer/tty classifiers,
NOT `claudeguard_darwin.go`. That file still builds for a native `go build` on a
mac, but it no longer ships in any published artifact.

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
