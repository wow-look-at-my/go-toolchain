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

Runs the composite action (`uses: ./`) with NO target inputs and
`autorelease: false`, so it exercises the exact default a consumer gets: ONE fat
APE (`go-toolchain`) covering linux/amd64, darwin/arm64 and
windows/amd64, plus `buildhost-artifacts.json`. The cosmo bootstrap downloads
the gosmopolitan toolchain from its default `?branch=master` and cold-compiles
its stdlib, hence the raised `timeout: 15`.

A trailing step asserts the shape the default exists to produce: the manifest is
schema 1 with exactly one artifact, its platform set is the three above, its
download filename is the plain `go-toolchain`, and `build/` contains NO file
matching the per-platform grammar. That last check is the one that stays honest
over time — a stray `<name>_<os>_<arch>` file would silently restore the
N-downloads-of-one-binary shape without failing anything else.

## build-everywhere and identical

`build` runs on ubuntu, and the three smoke jobs run THAT one binary on linux,
macOS and Windows. So the smoke jobs answer "does ubuntu's APE run everywhere",
which is only the same question as "does what we ship run everywhere" if every
host builds the same bytes. Nothing checked that, and until `-trimpath` and
`-ldflags=-buildid=` landed nothing could: the checkout path and the toolchain's
own content ID both reached the build-ID notes. See
[MATRIX.md](MATRIX.md) for the measurements and what each flag closes.

`build-everywhere` runs `matrix --no-benchmark` on all three hosts and hands
each result off under `ape-<origin>`; `identical` downloads them and compares
the other two against linux. `fail-fast: false`, so one host failing still
reports the others.

Every leg runs the SAME command, linux included, rather than reusing `build`'s
result. That costs one extra build and buys an unambiguous gate: a difference
is then the host, never the invocation. It also does not go through
`uses: ./` — the composite action installs itself with `sudo`, which a Windows
runner has not, which is why the smoke jobs stage the APE by hand too. Caching
is off in this job: it changes how long a build takes and never what it emits,
so leaving it out removes a variable rather than adding one.

A missing hand-off fails rather than passing on the survivors: comparing the
hosts that answered would report green for a property no host was checked on.
`publish` needs `identical`, so a build that is not reproducible never ships.

One compiler builds all three. gosmopolitan publishes on every green push, so a
run that spans a publish resolved a different fork on each leg and `identical`
read that as a host difference. `host-build` resolves the release once with
`go-toolchain version cosmo` and exports it to each leg as
`GO_TOOLCHAIN_COSMO_VERSION`; a probe that cannot name a release fails the step
rather than letting the legs pick their own. See [CMD.md](CMD.md) for how the
pin reaches the download URL and the cache key.

**Windows is red until the fork publishes for it.** The APE cannot complete an
HTTPS request on an NT host, so it cannot download the toolchain, and buildhost
serves no `gosmopolitan` windows/amd64 at master. Both are fixed by
gosmopolitan's crypt32 root-store work and its windows publish leg; this job
goes green when that merges. It is the same blocker smoke-windows already
reports, not a new one.

## The three smoke jobs

Each is `timeout-minutes`-bounded and downloads the `go-build-build` hand-off
the `build` job uploaded, via `wow-look-at-my/actions@cache-download#latest`
(run-keyed cross-OS cache wrapper; the download `path` is the destination
directory). The action names its hand-off `go-build-<job id>` per calling job,
with a `.m<job-index>` suffix per leg when the caller is a matrix job, so
concurrent same-run saves never collide on one key.

They EXECUTE throwaway copies of the artifacts in `dist/`, never the downloaded
file itself.

All three run the SAME file, `dist/go-toolchain` — there is one
artifact now, and each job proves it boots on that host.

**linux** — APE magic `MZqFpD`, then `version`, `--help`, host detection, and
the FULL default pipeline in a tiny module under the APE. The agent-output-guard regression is a
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

### smoke-macos: 5 of 10, and what the failing five are waiting on

The job is 5/10 (run 31827754447). Not worked around, and not a reason to
weaken it — the gate was deliberately strengthened to run the full pipeline
under the real published APE, which is what surfaced all of this.

**The five reds are the agent output guard.** `inspectFD` classifies stdout
through `/proc/self/fd`, which a darwin host does not have, so it returns at
its first statement and the guard never refuses. Closing that needs
gosmopolitan's `F_GETPATH`/`SO_PEERCRED` on master, and then the darwin branch
of `inspectFD` written here — in that order. `docs/AGENT-OUTPUT-GUARD.md` has
the chain and why the ordering is not negotiable.

Merging `is-this-an-agent`'s host dispatch moved NONE of the five, and could
not have: `agent.CommPPID` is called inside the socket branch, downstream of
the readlink that already failed. It was a real prerequisite for the socket
cases, just not a sufficient one for any of them.

**The five greens are load-bearing, not incidental:**

- `version host` answers `host: darwin (via runtime)` inside dats' seatbelt
  sandbox and outside it. `runtime.CosmoHostOS()` reads the runtime's own
  `__hostos`, which the APE entry stub records before any Go code runs and
  every syscall dispatches on, so no sandbox can deny it. It landed in the
  fork and `hostSignalFunc` now carries it, ahead of uname and the filesystem
  probes; those remain for a host the fork has no port for. Both assertions
  stay as regression cover, so an unwired seam fails CI instead of silently
  answering "linux" on a Mac.
- The INOPERATIVE banner fires. It is the only signal a human on that host
  gets while the guard is blind, and dats reports a failing test's unmet
  expectation rather than its actual stderr — so without a positive assertion
  it could regress leaving no trace in any log.
- `--help` and the two `version` exemptions prove the APE loads and dispatches
  on macOS at all.

**Windows** — magic, `version`, `--help`, host detection, a positive assertion
that the agent output guard is blind here and SAYS so, and the pipeline running
as far as the cosmo bootstrap. Two dimensions it cannot match, both fork gaps
rather than choices: the guard cannot fire (the classifier reads /proc), and the
pipeline cannot complete (no gosmopolitan windows/amd64 toolchain on buildhost,
and no DNS from an APE on NT). The step section below pins both, so each goes
red the day it lifts.

It used to stop at `--help`, on the grounds that gobootstrap downloaded
`go<version>.<os>-<arch>.tar.gz` and go.dev serves windows archives as `.zip`.
That reason is gone — the fork is the only toolchain now — but the platform
whose payload had been dying in package init was still the one asserting the
least.

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

## Vet self-heals against export data it cannot read

`src/cmd/exportdataretry.go`. The type-check reads each dependency's export
data — its compiled API — instead of its source. When go/types rejects that
data, the report is a cascade of "redeclared in this block" and undefined
symbols in a package the change never touched. It reads exactly like a source
error, which is why several runs were re-run as flakes before the signature was
recognized.

Two different things put it there, and neither is the source in front of you:

- A **damaged cache entry**, served by the shared GOCACHEPROG tier or by
  cmd/go's own on-disk cache.
- **Export data the importer cannot represent.** The importer is
  `golang.org/x/tools`, compiled into this binary against the `go/types` of
  whatever toolchain built it. The gosmopolitan fork is ahead of that toolchain,
  and its stdlib uses language features the older `go/types` refuses — a generic
  method (`func (r *Rand) N[Int intType](n Int) Int` in `math/rand/v2`) panics
  `NewSignatureType` with "function with type parameters cannot have a
  receiver". No cache is involved: the data is correct and the reader is old.

`go.mod`'s `go 1.27` is the fix for the second one, and it is a floor rather
than a preference. CI's `actions/setup-go` reads `go-version-file: go.mod`, so
the directive is what decides which `go/types` gets linked into the binary that
does the type-checking. Built against go1.26 it fails both ways: the import
panics as above, and reading the same package's source instead only trades the
panic for `method must have no type parameters` plus `file requires newer Go
version go1.27 (application built with go1.26)` across the fork's stdlib. Keep
this directive at or above the fork's Go version.

There are **two** reports, and which one appears depends on how far the decode
got before it hit the damage:

- `could not import <pkg> (invalid package name: "")` — the entry's header is
  unreadable, so the package has no name to report.
- `could not import <pkg> (reading <cachefile>: internal error in importing
  "<pkg>" (function with type parameters cannot have a receiver); please report
  an issue)` — the header decoded, so go/types names the package and then
  chokes on the type graph inside it. The "please report an issue" wording
  makes this one read like a toolchain bug rather than a cache problem.

Both are recognized. Matching only the first left the second surfacing as a
genuine compile error against untouched code (`could not import
math/rand/v2`), which is not something a reader can act on.

`RunTestsWithCoverage` detects either report and retries the vet phase ONCE
through `vet.RunFromSource`, which adds `packages.NeedDeps` so every dependency
type-checks from its own source. That takes no export data as input, so an
importer cannot be asked to read anything, and it covers both causes at once —
whereas dropping the shared tier alone leaves cmd/go's on-disk cache and the
importer exactly where they were. `GOCACHEPROG` is unset for the rest of the run
alongside it, which rules the shared tier out for the phases that follow. The
retry costs one source type-check of the dependency graph and only runs after
the fast path has already failed.

It warns each time it fires, naming the packages **and which of the two
signatures matched**, so a tier that is systematically serving bad entries shows
up in logs instead of being absorbed. A retry that hits the same report stops
the run with a message saying so: since that path read no export data, neither
`go clean -cache` nor a stale importer explains it.

Bounded by construction: the retry is a single call on the failure path, so it
can happen at most once.

## A test binary is built for the host, never for cosmo

`runner.Config.WithHostTarget` assigns `GOOS`/`GOARCH` from `hostos.GOOS()` and
`runtime.GOARCH` on every `go` invocation whose output has to RUN here: the test
run, the benchmark run, the compile check, and the `go list` calls that choose
what those cover.

The fork's default `GOOS` is cosmo, and `go test` fork/execs the binary it just
built. A fat APE bootstraps through a shell header, which `execve` never reads,
so the kernel rejects it and every package fails identically:

```
fork/exec /tmp/go-buildNNN/b586/trace.test: exec format error
FAIL	github.com/wow-look-at-my/go-toolchain/src/trace	0.000s
```

This is not a hole in the APE-only rule. That rule governs what the pipeline
SHIPS (`docs/MATRIX.md`); a test binary is a throwaway that must execute on the
machine that built it. The compiler is still the fork either way.

Known gap: the up-to-date fast exit (`src/cmd/uptodate.go`) fingerprints the
file list `go list` reports, and that list is per-GOOS. Vet reads the cosmo
variant while the tests read the host variant, so a file excluded from the one
`go list` it runs does not bust the fingerprint. Picking a variant is not the
fix — the fingerprint has to cover both.

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

## Step notes moved out of ci.yml

The one-line comment limit in ci.yml pushed these out of the workflow file. Each
section carries the text that used to sit above the named step.

### Provision the sandbox backend (bubblewrap)

The dats phase sandboxes every suite command (dats' default), and the
backend it picks decides what those commands can reach. Without
bubblewrap it falls back to docker, which runs them in a container --
no host Go for the bootstrap, and a ~350ms + image-pull tax per
command. Installing it is what dats' own error message tells you to do
on Linux, and the sysctl is the ubuntu-24.04 default that denies an
unprivileged user namespace (silently, until bwrap fails its probe).
The last line is the gate: an unusable bwrap fails the job here, with
its own error, instead of degrading to the fallback unnoticed.

### GITHUB_TOKEN: ${{ github.token }}

Dependency-graph submission needs a token with contents: write
(granted at the workflow level above). A bare `go run ./src` does
not inherit one, so pass it explicitly. Without it, submission can't
authenticate and fails loudly -- this is what wires the feature up.

### env

build/ in THIS job is the fat APE, because the fork is the only compiler
and every build emits one. Executing it here is safe because the step is
a shell: the APE bootstraps through a shell header, and only a raw
`execve` -- what `go run` and `go test` do to what they build -- cannot
read it. The APE never rewrites its own file, so the copy onto
`/usr/local/bin` needs no ordering dance.

### GITHUB_TOKEN: ${{ github.token }}

The second build re-invokes go-toolchain, which also submits the
dependency snapshot; give it the same token so it succeeds rather
than warning. Same job + correlator as the first submission, so it
replaces it (idempotent) rather than duplicating.

### if [ "$elapsed" -gt 60 ]; then

60s is derived, not guessed. On an UNCHANGED build graph (1629
actions, 98% cache-satisfied) this step measures 38-47s depending
only on which runner it lands on -- a package the commit never
touched swings 9.1s to 14.6s across those runs. A budget near 45s
therefore fails on runner speed, and a gate that cries wolf stops
being read on the run where the number is real.
What this gate is for is a cache that has stopped serving, and that
floor is far away: the cold first build in this same job is
~190-200s, so 60s still fails a broken cache by more than 3x.
Before raising it again, check the two signals this job already
prints -- cache-satisfied percentage and the poison tripwires. High
and zero mean a slow runner, and the limit is the thing to re-derive
from fresh numbers; a real cache failure does not look like this.

### Cross-compile socketharness

socketharness reproduces a coding agent's own tool-execution plumbing
(a socketpair for a child's stdio, not a bare pipe -- see
docs/AGENT-OUTPUT-GUARD.md) so smoke-linux/smoke-macos can prove the
actual reported bug against the real shipped binaries. Cross-compiled
here (this job already has Go set up) rather than via `setup-go` on
smoke-macos, which would put Go on that runner's PATH before the
"Full pipeline" step and quietly defeat the whole point of that job:
proving go-toolchain's OWN bootstrap works on a genuinely Go-less mac.

### build

Build + test via the composite action with NO target inputs, which is
exactly what a consumer gets: ONE GOOS=cosmo fat APE
(go-toolchain) covering linux/amd64, darwin/arm64 and
windows/amd64, plus the buildhost-artifacts.json manifest that publishes it
as a single multi-platform artifact. No per-platform copies, no native
cross-compiles.

Publishing is NOT done here (autorelease: false): the dedicated `publish`
job below is the single publish path, gated on the three smoke jobs that
actually EXECUTE the APE on linux, macOS and Windows runners -- a broken
APE can fail loudly but can never be released.

### Provision the sandbox backend (bubblewrap)

The dats phase sandboxes every suite command (dats' default), and the
backend it picks decides what those commands can reach. Without
bubblewrap it falls back to docker, which runs them in a container --
no host Go for the bootstrap, and a ~350ms + image-pull tax per
command. Installing it is what dats' own error message tells you to do
on Linux, and the sysctl is the ubuntu-24.04 default that denies an
unprivileged user namespace (silently, until bwrap fails its probe).
The last line is the gate: an unusable bwrap fails the job here, with
its own error, instead of degrading to the fallback unnoticed.

### Download host binary

Explicit name (host-build's "Upload host binary" hand-off): the strict
cache-download hard-fails a nameless pick whenever the RUN holds
several hand-offs, and on re-run attempts the go-build-* hand-offs
from an earlier attempt's `./` action step already coexist with
host-go-toolchain (run-scoped keys with cross-attempt fallback), so
"only one saved at this point" only ever held on attempt 1.

### uses: ./

The go-toolchain action itself cache-uploads build/ under the per-job
name `go-build-<job id>` on every run (unconditional) -- here that is
`go-build-build`, which the smoke-linux/macos/windows and publish
jobs below cache-download. The job id in the name keeps concurrent
same-run invocations (in other repos: the linux + darwin two-job
pattern) from colliding on one run-scoped key; there is no standalone
upload step here.

### timeout: '15

The cosmo target additionally downloads + extracts the gosmopolitan
toolchain and cold-compiles its stdlib; the default 10 minutes is
too tight for a cold runner.

### smoke-linux

Cross-OS smoke of the actual release artifacts: download the build-output
hand-off the `build` job uploaded (exactly what `publish` will ship) and RUN
the APE on each host OS. APEs self-assimilate on first exec -- they
rewrite their own header in place to the host's native format -- so every
job runs a throwaway copy, never the downloaded file itself. These jobs
have no checkout, so build/ lands in an otherwise empty workspace.

### uses: actions/checkout@v7

Only for the dats fixture .github/dats-fixtures/*.dats copied in
below -- this job otherwise runs entirely off the downloaded release
artifact.

### Download build outputs hand-off

Explicit name on purpose: by this point the run holds SEVERAL
hand-offs (host-go-toolchain from host-build, the per-job
go-build-build, and the deprecated bare go-build alias), so a
nameless self-discovering download would be ambiguous here. Same
for smoke-macos/smoke-windows/publish below.

### Download socketharness hand-off

socketharness reproduces a coding agent's own tool-execution plumbing
(a socketpair for a child's stdio, not a bare pipe -- see
docs/AGENT-OUTPUT-GUARD.md) so the guard fixture below can prove the
actual reported bug against the real shipped APE, not a unit test's
in-process fake. Cross-compiled in host-build (which already has Go
set up) rather than via setup-go here -- see smoke-macos, where
installing Go on that runner would defeat the point of that job.

### Stage the APE

Staging only. What the artifact must BE (the APE magic), that it runs,
and which host it detects are assertions, so they live in
.github/dats-fixtures/smoke-linux-agent-output-guard.dats, which the
pipeline step below runs against this same copy.

### Host detection (outside the sandbox)

The mirror of the same step in smoke-macos and smoke-windows: each
host pins its own answer, so all three jobs assert the one thing every
host-specific choice hangs off. This one must say `host: linux`, and
never GUESSED. Its sandboxed twin is in the dats fixture below --
worth having both, because the probes' fallback IS "linux", so the
sandboxed assertion alone could pass here for the wrong reason.

### GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED: '1

The smoke module is a synthetic consumer WITHOUT the org cache
credentials (no secret-server step here on purpose); this documented
knob downgrades the in-CI "caching not configured" error to a
warning. The repo's own host-build/build jobs keep the shared cache.

### cp "$RUNNER_TEMP/gt-ape" ./gt-under-test

The agent output guard regression lives as a committed dats
fixture (see smoke-macos), not hand-rolled bash: the released
binaries ARE the cosmo APE, so the guard must fire in THIS
artifact -- a GOOS=linux unit test cannot prove that (the guard
once shipped as a `_linux.go` no-op while unit tests stayed
green). Staged inside the module root so dats' sandbox (bwrap)
can reach it, same reasoning as smoke-macos.

### $RUNNER_TEMP/gt-ape

Full default pipeline: bootstraps an official Go toolchain if the
runner's is too old, then tidy/vet/test/coverage/build, then the
dats phase above.

### smoke-macos

macos-latest is arm64, and darwin/arm64 is in the APE's platform set, so
the APE is what ARM64 macs download. This job therefore runs the FULL
default pipeline under it -- the consumer-critical gate for mac users.

This gate is deliberately not reduced. It previously ran the full pipeline
against a native darwin/arm64 carve-out because the pipeline wedged AT EXIT
under the APE on macOS (issue #276): the gosmopolitan runtime ran
unix-socket fds blocking with no netpoller on darwin hosts, so the cache
daemon's Listener.Close deadlocked against its own Accept, blocked in raw
accept4(2), which a close never wakes on XNU. The fork's darwin netpoller
is a kqueue port now, so the deadlock should be gone; a red here is the
honest answer that it is not, and the job's timeout bounds the hang.

### uses: actions/checkout@v7

Only for the dats fixture .github/dats-fixtures/*.dats copied in
below -- this job otherwise runs entirely off the downloaded release
artifact.

### Download socketharness hand-off

See smoke-linux for why this is a download, not a local build: no
setup-go here, deliberately -- this job's whole point is proving
go-toolchain's OWN bootstrap works with NO Go on this runner's PATH.

### Stage the APE

Staging only -- the assertions about this artifact live in
.github/dats-fixtures/smoke-macos-agent-output-guard.dats.

### Host detection (outside the sandbox)

One APE runs on several hosts, so everything host-specific it does --
toolchain archives, brew paths, and the agent output guard's entire
classifier -- hangs off hostos.Detect(). It answers from
runtime.CosmoHostOS(), which no sandbox can deny; behind that sit the
uname and filesystem probes, whose fallback is "linux", so a regression
that unwires the seam answers "linux" ON A MAC and every dependent
decision is silently wrong. Assert it here, UNSANDBOXED; the dats
fixture below asserts the same thing from inside dats' seatbelt
sandbox. Both must say darwin.

This one assertion cannot move into a dats file: dats runs every
command sandboxed, and only --no-sandbox on the RUN turns that off,
which a suite may not decide for itself. Its sandboxed twin is a dats
test, as is everything else this job checks.

### cp "$RUNNER_TEMP/gt-ape" ./gt-under-test

The agent output guard regression lives as a committed dats
fixture (.github/dats-fixtures/smoke-macos-agent-output-guard.dats),
not hand-rolled bash: go-toolchain links dats in and runs any
dats/ suite found (recursively) in the module it is building --
there is no separate suite-running step, which is exactly why
this fixture is copied in rather than checked in under this
repo's OWN dats/: a suite asserting darwin-host behavior would
also run (and fail) during this repo's own linux build/host-build
jobs, which discover every dats/ suite recursively with no
filtering. dats sandboxes every command to the module root, so the
binary under test must live INSIDE it: $RUNNER_TEMP is invisible to
a sandboxed command (same reasoning as dats/README.md's
build/.dats-stage/ staging).

### $RUNNER_TEMP/gt-ape

Full default pipeline: macos-latest has no Go on PATH, so this is
the job's first real bootstrap (downloads, extracts and probes
go 1.24 from go.dev), then tidy/vet/test/coverage/build, then the
dats phase above.

### Agent output guard over a socket, unsandboxed

The same two socket cases the fixture above runs, but OUTSIDE dats --
the shape a real opencode user has, since nothing sandboxes them. The
two are not redundant: seatbelt is itself a variable the classifier's
probes answer differently under, so a disagreement between this step
and the fixture localizes the defect to the sandbox rather than to the
guard. Runs on always() so it still reports when the pipeline above
went red.

### printf 'module example.com/sockprobe\n\ngo 1.24\n' > "$probe/run/go.mod

A go.mod in the RUN DIRECTORY ITSELF, or the child never reaches the
guard: with no go on PATH, main.go's bootstrap reads the version to
fetch out of ./go.mod and exits before cobra runs when there is
none. It does not walk up (MEASURED: one directory above was not
enough). The dats fixture never hits this: its suites run from
inside go-toolchain's own pipeline, which has a go by then. The
version
matches what the pipeline step above already cached, so this
bootstraps from disk instead of downloading a second toolchain.

### cp "$GITHUB_WORKSPACE/harness/socketharness-darwin-arm64" "$probe/run/harness

A copy per case, taken from the pristine artifact rather than from
$RUNNER_TEMP/gt-ape: that one has been executed several times by the
steps above, and an APE rewrites its own file on first exec, so a
copy of it is no longer the thing a mac user downloads. The dats
fixture copies pristine too, which is what makes the two comparable.

### Run the APE PE payload

These assertions stay in the workflow because dats cannot run here:
its sandbox backends are bwrap, sandbox-exec and docker, and
windows-latest has none, so a suite would fail before its first
command. Linux and macOS assert the same properties from
.github/dats-fixtures/*.dats. Where the suite is the only
difference the assertions are still made, step by step, below --
this job covers the same ground as the other two apart from the
guard, which NT genuinely cannot classify.

### [ "$(head -c 6 dist/go-toolchain)" = "MZqFpD" ]

An APE is simultaneously a valid PE, whose embedded payload is a
native windows/amd64 build. The .exe name is given here at copy
time: NT dispatches on the extension, and the published artifact
carries none.

### ./smoke/gt-ape.exe --help

Smoke contract (owner-ruled): gt must resolve the machine's
EXISTING Go (windows-latest ships an image-default Go) via its
own exec.LookPath on the NT side and bootstrap it -- broken
pre-gosmopolitan#63 (unix-style ':' PATH walk with no .exe
suffixing on NT hosts), fixed in fork v237+. Do NOT provision Go
in this job (no setup-go -- that bypasses the bootstrap
requirement); do NOT exempt commands from the bootstrap. If the
image drops Go, the red is honest -- escalate to the owner.

### Host detection

The mirror of the same step in smoke-macos. One APE runs on every
host, and what it detects decides every host-specific choice it
makes, so each smoke job pins its own answer: `host: windows`,
and never GUESSED.

This step was red the moment it was added, which is what it is
for. `runtime.GOOS` is `cosmo` on NT too -- the APE's windows
payload is a cosmo build, not a native one -- so `Detect()` ran
the cosmo probe chain, where `syscall.Uname` is ENOSYS and both
filesystem probes are absent, and returned the `"linux"` default.
Every host-specific choice was then made for the wrong host: the
`bin/go.exe` suffix, the buildhost slot the fork downloads from,
and the guard's classifier dispatch. The cure is
`runtime.CosmoHostOS()` (see the smoke-macos section above); the
answer is now `host: windows (via runtime)`.

### The pipeline reaches the cosmo bootstrap and names its blocker

smoke-linux and smoke-macos drive a synthetic consumer through the
whole pipeline, proving the shipped APE can tidy, vet, test and
build a real module on that platform. NT cannot do that today, for
two gaps that both live in the fork, not here:

1. **buildhost carries no gosmopolitan windows/amd64 toolchain.**
   The publish job builds `linux/amd64` and `darwin/arm64`, each on
   its own runner, because distpack packages what a HOST build
   produced. Every other slot is a 404, so cosmobootstrap has
   nothing to download for an NT host.
2. **The APE cannot resolve DNS on an NT host.** `lookup
   dl.pazer.build on [::1]:53: i/o timeout` -- `[::1]:53` is
   netgo's fallback nameserver when `/etc/resolv.conf` cannot be
   read, so the resolver never asks Windows and never reaches a
   real nameserver. The fork's own PLATFORM-STATUS.md lists
   off-host networking and DNS from NT as missing, and its
   DEBUGGING.md carries this as a hypothesis; the log line above is
   the confirmation.

So the step asserts the reachable half: the pipeline runs, gets as
far as the cosmo bootstrap, and fails there naming one of exactly
those two blockers. A run that SUCCEEDS is red, and says to replace
this step with the assertion the other two jobs make. A failure
before the bootstrap is red. A bootstrap failure with any third
signature is red -- otherwise a real regression would hide behind a
gap we already know about.

An earlier revision of this job did assert the full pipeline, on
the belief that buildhost serves the fork for every os/arch. It
does not. That claim was never checked, and the step could not
have passed.

### Agent output guard is inert on NT

The one dimension Windows cannot match. The APE is a cosmo build
everywhere, so claudeguard_proc.go is the classifier on NT too --
and it reads /proc, which NT does not have. The readlink fails,
`unclassifiableSink` sees a host that is not linux, and
`blindClassifierSink` allows the run. That is a documented
decision, not an accident, and this step asserts both halves of
it. A bare pipeline run with captured stdout under CLAUDECODE=1
must NOT print "refused to run"; when someone teaches
`inspectStdout` to classify a Windows handle, this step goes red
and asks to be turned into the refusal assertion the other two
jobs make.

It must also print the INOPERATIVE banner naming `windows`. That
banner is the only thing a human on this host gets while the guard
is blind, and dats-style absence assertions cannot catch it going
missing. It doubles as a second reading of host detection from
inside the guard: the banner named `linux` here until
`runtime.CosmoHostOS()` was wired, which is the same defect the
Host detection step above caught.

### publish

The single publish path. Gated on the cross-OS smoke jobs above so a build
whose APE cannot actually run on linux/macOS/Windows is never released.
Downloads the same build-output hand-off the smoke jobs ran and publishes
build/ straight to buildhost (no GHA artifact involved), authenticating
with a GHA OIDC token (hence id-token: write).
