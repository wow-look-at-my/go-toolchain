# The org pin check (`src/cmd/orgpins.go`)

Depth for the "Dependency handling" line in the [README](../README.md#features).

An org dependency has no version of its own. gosmopolitan's `cmd/go` resolves `github.com/wow-look-at-my/...` to the head of a branch. It takes the branch this repository is on when the dependency has one of that name. It takes the dependency's default branch otherwise. The files on disk keep a placeholder.

A frozen version defeats that. It names one commit of another repository. Nothing moves it. So a consumer builds old code and reads the result as current. Every repository in the org runs this pipeline, which is what makes the pipeline the place to enforce the rule.

The pipeline repairs a pin before `go mod tidy`, and logs each repair:

| Pin | Repair |
| --- | --- |
| a version after an org path in `go.mod` or `vendor/modules.txt` | the placeholder for that path (`v0.0.0`, or `vN.0.0` for a `/vN` path) |
| an org line in `go.sum` with a pinned version | the line is dropped, and tidy writes the new sum |
| an org action at `@vN` or a commit | `@master` |

An org submodule with no `branch` is the only pin that still fails the run. Nothing tells the pipeline which branch it must follow.

## What counts as a pin

| File | Accepted | Refused |
| --- | --- | --- |
| `go.mod`, `go.sum`, `vendor/modules.txt` | `vN.0.0` for the path's major | a dated pseudo-version, a release tag |
| `.gitmodules` | an org submodule with a `branch` | an org submodule with none |
| `.github/workflows/*.yml`, `.github/actions/*/action.yml` | `@master`, and the org's `@name#latest` orphan tags | `@vN`, a 40-character commit |

A third-party dependency is not looked at. It keeps the version it names.

## Naming a branch

A go.mod line names a branch to send one module somewhere other than where the rest of them go. The comment goes on the line the version lives on. A fork consumed through a `replace` therefore carries it there:

```go
require github.com/wow-look-at-my/foo v0.0.0 // branch=v1
require github.com/wow-look-at-my/bar v0.0.0 // indirect; branch=v1

replace charm.land/bubbletea/v2 => github.com/wow-look-at-my/bubbletea/v2 v2.0.0 // branch=v1
```

`cmd/go` reads the name. This pipeline does not. The version token beside the name is still the placeholder. So a named line is not a pin.

A name is resolved the same way the branch this repository is on is. A dependency with no branch of that name takes its default branch. So the pin follows the code once a merged pull request deletes the branch it was opened from.

## One resolution per CI run (`GOORGPIN`, `src/cmd/orgpinenv.go`)

`cmd/go` resolves a branch head on every invocation. A dependency that gets a commit while a run builds then reaches different jobs, and different passes of the self-hosted build, at different commits. The `identical` job then finds APEs that differ. A later pass can miss a `go.sum` entry.

`GOORGPIN` holds whitespace-separated `modulepath=version` entries. The fork's `cmd/go` builds each listed org module at that version instead of its branch head. The fork honors the variable only when `GITHUB_ACTIONS=true` and no coding agent is detected. Anywhere else a set `GOORGPIN` is a hard error from the go command.

- In CI, with `GOORGPIN` empty, the pipeline resolves every org module once, after the go command is set up and before any phase runs. It runs `go list -mod=readonly -m all` in each module the run builds and keeps the org entries, including the target of a `replace`. It exports the result, so every go command, every pass of the self-hosted build and each re-exec inherits it.
- A `GOORGPIN` the workflow set is kept as it is.
- A failed resolve fails the run. So do modules of the run that resolve one org module to different versions.
- Outside CI the pipeline neither resolves nor sets pins. Nothing lets a local run set or honor them.
- The pins are part of the up-to-date fingerprint. As a result, a moved dependency is a new input.

Each run logs the pins it builds, one per line:

```
⇒ org pins, resolved at the start of this run:
   github.com/wow-look-at-my/dats=v0.0.0-20260910122754-5dfcc0b24b09
   github.com/wow-look-at-my/slopfix=v0.0.0-20260926074833-23586e671eee
```

With pins from the workflow, the first line reads `⇒ org pins, set by the workflow:`.

A workflow with several jobs that build must resolve once for all of them. One pin job computes the pins and exposes them as a job output. Every job that builds `needs:` it and sets `GOORGPIN` at job level. See [ACTION.md](ACTION.md#5-one-set-of-org-pins-for-a-multi-job-workflow).
