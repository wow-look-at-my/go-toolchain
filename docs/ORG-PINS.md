# The org pin check (`src/cmd/orgpins.go`)

Depth for the "Dependency handling" line in the [README](../README.md#features).

An org dependency has no version of its own. gosmopolitan's `cmd/go` resolves `github.com/wow-look-at-my/...` to the head of a branch. It takes the branch this repository is on when the dependency has one of that name. It takes the dependency's default branch otherwise. The files on disk keep a placeholder.

A frozen version defeats that. It names one commit of another repository. Nothing moves it. So a consumer builds old code and reads the result as current. Every repository in the org runs this pipeline, which is what makes the pipeline the place to check the rule. A finding fails the run before `go mod tidy`.

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

A named branch never falls back to the default branch. A name that stopped resolving fails the build, which is what a merged pull request does to the branch it was opened from.
