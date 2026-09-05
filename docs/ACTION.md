# action.yml — the composite GitHub Action

What `wow-look-at-my/go-toolchain@master` does, in order, and what a consuming
workflow has to grant it.

## 1. The all-builds shadow guard

The first step runs `wow-look-at-my/actions@no-all-builds-job#latest`, which
fails the job if any workflow job is named `all-builds`. That name belongs to
the org's required status, posted by the required-builds-manager app. A job
wearing it cannot satisfy the gate and only shadows the real status in the UI.

Since `no-all-builds-job#3` the guard scans **the run's jobs** (Actions API) and
**the head commit's check runs** (Checks API), and it **fails closed** when it
cannot scan. The calling workflow's token therefore has to grant
`actions: read` and `checks: read` — private repos 403 without them; public-repo
reads pass scope-less. Both are in the documented consumer permissions block.

That `actions: read` is the guard's requirement, not autorelease's.

## 1b. The comment-wall guard

The next step runs `wow-look-at-my/actions@yaml-comment-block#latest`. More than
one comment line in a row in a GitHub Actions YAML file fails the job. It scans
the calling repo's whole local call chain: every workflow file, every
`action.yml` at any depth, and everything they reach through `uses: ./...`. A
`uses:` into another repository is listed in the log and checked where it lives.

A block is a maximal group of comment lines separated by nothing except blank
lines, so a paragraph break does not split a wall. A `#` inside a `run:` script
counts. The limit is a constant and no input raises it. The action needs no
permissions, only a checkout.

## 1b2. The tests-in-YAML guard

The step after it runs `wow-look-at-my/actions@no-tests-in-yaml#latest`. A test
written inside a `run:` script fails the job. A workflow step is a scheduler: an
assertion living there runs only on a runner, only after a push, and only in
that one repository. So nobody can run it against a change before sending it.

Three rules, each reading only `run:` scripts. A redirect or heredoc writing a
file that is a test by name (`*_test.go`, `*.test.ts`, `*.dats`, and the rest).
A comparison paired with a failure on one line — `grep -q … || exit 1`,
`if ! grep …`, `[ … ] || { … }`, and the annotate-and-exit line a `case` arm
uses. A shell function named `assert*`, `expect*`, `must*` and friends.

A step that merely runs a command fails on its own exit code and matches
nothing, which is what keeps an ordinary build silent. No input turns a rule
off. It scans the same local call chain the comment-wall guard does, needs no
permissions beyond a checkout.

## 1b3. The APE binfmt handler

`.github/scripts/register-ape-binfmt.sh` writes one `binfmt_misc` entry on a
Linux runner:

```
:APE:M::\x4d\x5a\x71\x46\x70\x44\x3d\x27::/bin/sh:
```

The magic is the header a fat APE opens with, `MZqFpD='`, and the interpreter is
`/bin/sh`. There is no APE loader to install: the header IS a shell script, so
the kernel handing the file to `sh` is the whole mechanism. What it buys is a
bare `execve` of an APE. Without the entry only a shell can start one, and `go
run`, `go test` and any exec from a program answer `exec format error` — this
repo's own CI has shipped that message.

The step needs root and a mounted `/proc/sys/fs/binfmt_misc`. A host that has
neither keeps working: it warns, names what is missing, and exits 0. So the entry is
a capability, never a requirement, and nothing downstream may assume it — see
[MATRIX.md](MATRIX.md). The step is skipped outright on macOS and Windows, which
have no such mechanism.

Registering is idempotent. An entry that is already present and enabled is left
alone. One that is present and disabled is reported rather than silently
counted as working, since a disabled entry execs nothing.

`dats/binfmt.dats` covers the contract: the script names its outcome, reaches
the same outcome twice, and never fails the job. It registers nothing itself —
the sandbox grants no root — and where an entry does exist it asserts the magic
and the interpreter.

## 1c. Installing the binary

The download goes straight to buildhost's `dl` endpoint with curl, and no npm is
involved. `--compressed` advertises `Accept-Encoding`, zstd included where curl
was built with it, so buildhost streams the stored zstd blob as-is and curl
decompresses client-side. The server never pays the decompression cost. Where
curl lacks zstd it just gets the plain binary. buildhost normalizes platform
aliases natively (`RUNNER_OS` Linux/macOS/Windows, `RUNNER_ARCH` X64/ARM64), so
those values pass through verbatim. It serves the branch tip `no-store`, so no
cache-buster is needed. Download, the one pre-install run, and the copy into
`/usr/local/bin` are one step. Nothing in the org's action set can write there
(the runner is not root), and dats' sandbox mounts only the standard paths. So a
split would only move the `sudo cp` into a second step.

**The URL carries no `branch=` pin.** buildhost's bare "latest" resolves against
the project's default branch. A pin would name a buildhost branch, not a git one, and would have to be
kept in step with an operator setting the API.

The install runs only on a successful download. A failure is reported rather
than hidden behind `|| true`, and it is non-fatal at that point. Gating with
`if` keeps `set -e` from skipping the probe and the source-build fallback below,
which surface the reason and decide whether the build fails.

**The binary runs once BEFORE the root-owned install.** Since the fat-APE
migration the linux and windows/amd64 slots serve an APE polyglot that
self-assimilates on first exec by reopening ITSELF read-write. That works while
the file is still runner-writable in `/tmp`. It is impossible for the non-root
runner once the file is root-owned in `/usr/local/bin`, which gives
`line 11: ... Permission denied`. After that run the installed file is a plain
native binary. For a native slot such as darwin/arm64 the run is only an early
version check. Its failures are tolerated with `|| true`, because the probe that
follows is the single pass/fail gate and it surfaces the reason.

The probe captures its output rather than discarding it. So the real reason the
binary is unusable is shown: a 404, a missing PATH entry, or a crash. A source
build happens only where the caller opted in. A silent fallback hides a
buildhost outage and ships a locally-compiled toolchain that can differ from the
released one.

A caller-provided `binary:` is staged through `/tmp` and pre-run once for the
same APE reason. Staging also keeps the caller's own file byte-identical. For a
native binary this changes nothing.

## 1e. The CodeQL permission check

`wow-look-at-my/actions@has-permission` reads `security-events` off the running
workflow file, and fails the build where the grant is missing. Reading the
declared block needs no token and spends no API call.

## 2. Secrets, then the build

Fetches secrets over OIDC, then runs `go-toolchain matrix`.

**What the target inputs build.** With `targets` unset — the default — the run
produces ONE fat APE covering `cosmo-platforms`
(`linux/amd64,darwin/arm64,windows/amd64`), published as a single
multi-platform artifact. The fat APE is the action's only native output.
`targets` adds wasm artifacts alongside it (`wasm/js`, `wasm/wasip1`), or
replaces it entirely with wasm alone by leaving `cosmo` out of the list.
There is no input that copies the APE onto per-platform artifact names; the
APE publishes under its own name, once.

## 3. Handing off `build/`

Every run ends by cache-uploading `build/` for downstream jobs.

**Per-job+build name** — {% raw %}`go-build-${{ github.job }}`{% endraw %}, plus
`.m<strategy.job-index>` for a matrix job and `.b<build>` for the build identity
(the sanitized `working-directory`):

```
{% raw %}
name: go-build-${{ github.job }}${{ matrix && format('.m{0}', strategy.job-index) || '' }}.b${{ steps.build-id.outputs.id }}
key:  cache-xfer-<run_id>-go-build-<job>[.m<idx>].b<build>-<run_attempt>
{% endraw %}
```

The matrix and strategy contexts *are* evaluable inside composite steps (the
runner's manifest schema allows both), and `matrix` is null for a non-matrix
job. So those suffixes collapse to the empty string for a single, non-matrix
build. The dots make the suffixes collision-proof against job ids. That cannot
contain dots, and `job-index` is stable across re-run attempts, so cross-attempt
restore fallback keeps working.

Being distinct per job, per matrix leg, AND per build is the point: concurrent
go-toolchain saves in one run — two jobs. The `.b<build>` suffix is what lets one job build
two things (e.g. a plugin and the marketplace-build CLI) without colliding.

**Downloading it** — `cache-download` with no `name` self-discovers the current
run's hand-off through the run-scoped key prefix, and emits a `::notice` naming
what it picked. So a consumer never has to know the producing job's id:

```yaml
- uses: wow-look-at-my/actions@cache-download#latest
  with:
    path: dist   # no name: self-discovers this run's hand-off
```

Nameless discovery is clean only when the run's hand-off set is unambiguous at
download time (the exact ambiguity semantics belong to `cache-download` — see
its docs). A run that saves several distinct hand-offs — several go-toolchain
jobs, a matrix go-toolchain job, or extra `cache-upload` hand-offs alongside the
build outputs.

**This is the only name saved.** The pre-build per-job name
`go-build-<job>[.m<idx>]` and the bare `go-build` alias are gone: each was a
second key that a multi-producer run raced on. So the second finisher's save
collided and had to be absorbed with `continue-on-error`. The action now saves
ONE hand-off, under that name; a download naming anything else restores
nothing. That is why this repo's own `identical`, `smoke` and `publish` jobs
spell `go-build-build.broot` in full. A consumer that still downloads either
legacy name gets a miss and must migrate to the name above.
`src/cmd/handoffname_test.go` pins both the template and the absence of any
second hand-off.

**Why the name carries the job id, a leg index, and a build identity.** The
hand-off runs on EVERY go-toolchain run, through the org cache-upload action,
which replaces `actions/upload-artifact`: cache storage is free and artifact
storage is billed. The name carries the calling job's id, so the cache key
(`cache-xfer-<run_id>-go-build-<job>-<run_attempt>`) is distinct per job.
Concurrent jobs in one run, the standard linux plus darwin pattern, can then no
longer collide. The old shared `go-build` name made the second finisher fail its
save with a 409 Conflict.

`github.job` alone does NOT distinguish matrix legs, so a matrix job's name
additionally carries the leg's `strategy.job-index` as `.m<index>`. The matrix
and strategy contexts ARE evaluable inside composite steps: the runner's
action-manifest schema allows both in step expressions. For a non-matrix job
`matrix` is null, so the suffix collapses to the empty string and the non-matrix
name stays byte-identical to what it was.

`github.job` also does NOT distinguish two go-toolchain invocations in the SAME
job. So the name additionally carries a build identity `.b<build>` derived from
the `working-directory` input (slashes and dots replaced with `-`, default `.`
becomes `root`). Two builds in one job therefore save distinct hand-offs and can
no longer 409 on a shared key.

The dots make the suffixes collision-proof against job ids. A job id cannot
contain a dot, so `go-build-<jobA>.m<i>` or `go-build-<jobA>.b<build>` can never
equal, or restore-prefix shadow, any `go-build-<jobB>`. `job-index` is 0-based in
matrix definition order and identical across re-run attempts of the same leg,
which keeps cache-download's cross-attempt fallback working.

A downstream job cache-downloads with NO name, which self-discovers the current
run's hand-off. That is the preferred mode when the run saves only one hand-off,
and it needs no knowledge of the producing job's id. A run carrying several
hand-offs needs an explicit `go-build-<uploader job id>.b<build>`, or
`go-build-<uploader job id>.m<index>.b<build>` such as `go-build-build.m2.broot`,
because discovery would otherwise be ambiguous. A matrix producer is always such
a case, because each leg saves its own name.

## 4. Autorelease, and the permissions it needs

`autorelease` (on by default) publishes the workspace `build/` **directly** to
buildhost, through `wow-look-at-my/buildhost`'s buildhost-publish action and its
local `path` input — no GitHub Actions artifact is involved.

**`autorelease_args` is parsed, not spread.** A composite `with:` block is static
YAML, so those args cannot be spread into the publish step dynamically. Each
recognized key is parsed into a step output and mapped onto an explicit
buildhost-publish input. An unknown key fails loudly, because a typo must never
be silently ignored. An empty input leaves every output empty, and
buildhost-publish treats an empty input as absent, so the publish stays
byte-identical.

**The grants are read before the build.** A missing `id-token: write`,
`deployments: write` or `artifact-metadata: write` used to surface as `Resource
not accessible by integration` AFTER the whole build had run. `wow-look-at-my/actions@has-permission` now reads each one in the
first seconds. It reads the running workflow file and resolves the
scope the way GitHub does. The job's own `permissions:` block, then the
workflow-level block when the job declares none. A missing grant fails the step
that reads it, and the error names the grant and the block it came from. The
check only runs where `autorelease` is on.

It replaced a set of empty-body `POST` probes that read a 403 off the live API.
Reading the declared block needs no token, spends no API call, and cannot be
confused by a failure that has nothing to do with permissions. It also removed
a fork-PR carve-out. The old `id-token` check read `ACTIONS_ID_TOKEN_REQUEST_URL`
out of the environment, which GitHub withholds on an external fork PR whatever
the workflow declares. So that case had to be skipped by name. The declared
block is the same on a fork.

The publish step itself needs only `id-token: write`. But publishing also
**registers a GitHub Deployment and posts an artifact storage record**, and
neither has an opt-out. So a job that autoreleases must additionally grant:

```yaml
permissions:
  id-token: write
  deployments: write        # the publish registers a GitHub Deployment
  artifact-metadata: write  # the publish posts an artifact storage record
```

Each fails the build without its grant — the build runs to completion and then
dies on `Resource not accessible by integration`. Job-level `permissions:`
blocks REPLACE the workflow-level one, so a job declaring its own must list
these alongside everything else it needs.

The one case that does not register is a publish whose target server is
loopback or plain http (buildhost's own e2e spawns one on
`http://localhost:18080`). A deployment asserts "this publish is live at
`<environment_url>`", which is false for a server nothing outside the runner
can reach. That is a property of the target, not an opt-out — every publish to
a real https server registers, and a failure there is fatal.

---

*Provenance: assembled from three near-duplicate `action.yml` bullets that had
accumulated in CLAUDE.md — the shape a file takes when every visitor appends.
They were not three topics but three generations of one, and merging them meant
resolving where they disagreed rather than keeping all three. The newest had the
current cache key (verified against `action.yml`) but had **dropped** the
`deployments: write` / `artifact-metadata: write` requirement the previous one
stated as mandatory. That requirement is real (`action.yml:43`, `README.md`, and
this repo's own `ci.yml` grants both) and is restored above. The oldest predated
the guard's API-scanning permissions entirely.*
