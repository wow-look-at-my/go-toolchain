# The org pin check (`src/cmd/orgpins.go`)

Depth for the "Dependency handling" line in the [README](../README.md#features).

gosmopolitan's `cmd/go` resolves `github.com/wow-look-at-my/...` to the head of a branch. It takes the branch this repository is on when the dependency has one of that name. It takes the dependency's default branch otherwise. It records that head's pseudo-version in `go.mod`, and every go command moves it to the current head. A CI run builds the head it locked for the run, whatever `go.mod` records. The contract is gosmopolitan's `docs/ORG-DEPS.md`.

So a version in `go.mod` is not a pin. This pipeline leaves it alone. A rewrite to a placeholder only makes the next go command write the head again, and the CI dirty check then fails.

A frozen version elsewhere names one commit of another repository. Nothing moves it. So a consumer builds old code and reads the result as current. Every repository in the org runs this pipeline, which is what makes the pipeline the place to enforce the rule.

The pipeline repairs a pin before `go mod tidy`, and logs each repair:

| Pin | Repair |
| --- | --- |
| a version after an org path in `vendor/modules.txt` | the placeholder for that path (`v0.0.0`, or `vN.0.0` for a `/vN` path) |
| an org line in `go.sum` with a pinned version | the line is dropped, because an org module has no sum |
| an org action at `@vN` or a commit | `@master` |

An org submodule with no `branch` is the only pin that still fails the run. Nothing tells the pipeline which branch it must follow.

## What counts as a pin

| File | Accepted | Refused |
| --- | --- | --- |
| `go.mod` | any version: the go command owns it | nothing |
| `go.sum`, `vendor/modules.txt` | `vN.0.0` for the path's major | a dated pseudo-version, a release tag |
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

`cmd/go` reads the name, and records the head of that branch beside it. This pipeline does not read it.

A name is resolved the same way the branch this repository is on is. A dependency with no branch of that name takes its default branch. So the pin follows the code once a merged pull request deletes the branch it was opened from.
