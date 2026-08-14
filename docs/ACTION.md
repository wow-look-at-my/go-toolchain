# action.yml — the composite GitHub Action

What `wow-look-at-my/go-toolchain@v1` does, in order, and what a consuming
workflow has to grant it.

## 1. The all-builds shadow guard

The first step runs `wow-look-at-my/actions@no-all-builds-job#latest`, which
fails the job if any workflow job is named `all-builds`. That name belongs to
the org's required status, posted by the required-builds-manager app; a job
wearing it cannot satisfy the gate and only shadows the real status in the UI.

Since `no-all-builds-job#3` the guard scans **the run's jobs** (Actions API) and
**the head commit's check runs** (Checks API), and it **fails closed** when it
cannot scan. The calling workflow's token therefore has to grant
`actions: read` and `checks: read` — private repos 403 without them; public-repo
reads pass scope-less. Both are in the documented consumer permissions block.

That `actions: read` is the guard's requirement, not autorelease's.

## 2. Secrets, then the build

Fetches secrets over OIDC, then runs `go-toolchain matrix`.

**What the target inputs build.** With none of them set — the default — the run
produces ONE fat APE covering `cosmo-platforms`
(`linux/amd64,darwin/arm64,windows/amd64`), published as a single
multi-platform artifact. `os` and `arch` are EMPTY by default; setting either
switches to one native binary per platform. `targets` replaces both with an
exact list. `cosmo-slots` is empty by default and copies the APE onto
per-platform artifact names — N identical files, N download links — for a
consumer that cannot yet resolve the APE itself.

## 3. Handing off `build/`

Every run ends by cache-uploading `build/` for downstream jobs.

**Per-job name** — {% raw %}`go-build-${{ github.job }}`{% endraw %}, plus `.m<strategy.job-index>`
for a matrix job:

```
{% raw %}
name: go-build-${{ github.job }}${{ matrix && format('.m{0}', strategy.job-index) || '' }}
key:  cache-xfer-<run_id>-go-build-<job>[.m<idx>]-<run_attempt>
{% endraw %}
```

The matrix and strategy contexts *are* evaluable inside composite steps (the
runner's manifest schema allows both), and `matrix` is null for a non-matrix
job, so those names stay byte-identical to what they were. The dot makes the
suffix collision-proof against job ids, which cannot contain dots, and
`job-index` is stable across re-run attempts, so cross-attempt restore fallback
keeps working.

Being distinct per job *and* per matrix leg is the point: concurrent
go-toolchain saves in one run — two jobs, or the legs of one matrix job — used
to 409 on a shared key.

**Legacy bare alias** — a second save under the bare name `go-build`, for
download-only consumers that still restore it (webhook-runner, buildhost,
api-cli, github-state-mirror, publish-ghcr callers). It is preceded by a
`::notice` deprecation annotation and marked `continue-on-error`, because a bare
key is inherently racy in a multi-producer run: first finisher wins and the
second save's conflict is absorbed. The strict per-job/per-leg save stays the
sole authoritative one. Proposed for removal once those consumers migrate to
`go-build-<uploader job id>`.

## 4. Autorelease, and the permissions it needs

`autorelease` (on by default) publishes the workspace `build/` **directly** to
buildhost, through `wow-look-at-my/buildhost`'s buildhost-publish action and its
local `path` input — no GitHub Actions artifact is involved.

The publish step itself needs only `id-token: write`. But publishing also
**registers a GitHub Deployment and posts an artifact storage record**, and
neither has an opt-out, so a job that autoreleases must additionally grant:

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
`http://localhost:18080`): a deployment asserts "this publish is live at
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
stated as mandatory; that requirement is real (`action.yml:43`, `README.md`, and
this repo's own `ci.yml` grants both) and is restored above. The oldest predated
the guard's API-scanning permissions entirely.*
