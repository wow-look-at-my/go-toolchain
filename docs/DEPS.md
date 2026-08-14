# Dependency handling

Depth for the "Dependency checking" line in the [README](../README.md#features) and step 3 of
[How It Works](../README.md#how-it-works). Three independent mechanisms run early in every
pipeline, all before `go mod tidy`; each rewrites `go.mod` in place, so the same rules as any
other pipeline mutation apply: locally you see the diff and commit it, in CI a resulting dirty
tree fails the build (`checkDirtyInCI`) with an actionable message.

## v0.0.0 repair (`FixBogusDepsVersions`, `src/cmd/depsfix.go`)

A `require` line with version `v0.0.0` is not a valid, fetchable pseudo-version — it shows up when
a dependency is added by hand (an editor auto-import, a copy-pasted line) instead of via `go get`.
Before `go mod tidy` runs (which would otherwise just fail on it), go-toolchain resolves the
module's default branch HEAD via `git ls-remote <url> HEAD` and rewrites the line to a real
pseudo-version (`vX.Y.Z-<timestamp>-<12-hex-hash>`).

## Same-org auto-update (`src/cmd/deps.go`, `src/cmd/depsreport.go`)

Every run asynchronously checks each direct dependency's pseudo-version against
`$GOPROXY/<module>/@latest`. For git-based (untagged) dependencies this proxy endpoint always
resolves to the module's **default branch**. A module that carries *any* semver tag resolves to
the highest such tag instead, which for a dependency tracked as a pseudo-version can point at an
older commit than the branch itself — `go get -u <module>@latest` then moves the require line
backwards. Branch tracking (below) is the supported way off that path. Any outdated dependency sharing the current module's
`host/org/` prefix (its own org — "trusted") is auto-updated in place via `go get -u` + `go mod
tidy`; anything else is only reported, with a `go get -u` hint.

## Branch-tracked dependencies (`src/cmd/depsbranch.go`)

Colloquially "the `//branch` comment" — spelled `go-toolchain:branch=` in the file, so searching a
checkout for `//branch` finds nothing. Search for `branch=` or read on.

The two mechanisms above always resolve to a module's *default* branch — there is no way to ask
either one to follow a different branch, and letting the org auto-updater run `go get -u` on a
dependency someone deliberately pinned to a non-default branch would silently drag it back onto
`main`. A `require` line opts out of both and into branch tracking with a trailing comment:

```go
require github.com/wow-look-at-my/foo v0.0.0-20240101120000-abc123def456 // go-toolchain:branch=v1
```

Every run re-resolves that branch's current HEAD via `git ls-remote <url> refs/heads/<branch>` and
rewrites the pseudo-version in place (the comment is preserved, so the pin stays declarative across
runs); `listDirectDeps` excludes any such line from the org auto-update path so the two mechanisms
never fight over the same require. The dependency still always resolves to one concrete,
`go.sum`-verified pseudo-version — go-toolchain does not do unpinned/floating dependency
resolution — the marker only tells it *which* branch's HEAD that pin should track, and a human
still reviews and commits every version bump the same way they would a manual `go get -u`.

Only direct (non-`// indirect`) requires are tracked; an indirect line carrying the marker is
skipped with a warning rather than silently ignored, since indirect dependencies are resolved
transitively and a per-line branch pin on one would not mean what it looks like it means. The
warning names the way that does work: a `replace` carrying the marker (see below) is main-module
only, so it applies to direct and indirect requires alike and pins the version that actually
reaches the build.

### Tracking a fork through a `replace`

A fork keeps upstream's module path — that is what makes it a drop-in — so it is consumed through a
`replace`, and the `require` line still names *upstream*. The marker therefore goes on the replace
line, where the version that actually reaches the build lives:

```go
require charm.land/bubbletea/v2 v2.0.8

replace charm.land/bubbletea/v2 => github.com/wow-look-at-my/bubbletea/v2 v2.0.0-20260812203640-351d2159f8d8 // go-toolchain:branch=master
```

The branch is resolved against the **replacement's** repository (`github.com/wow-look-at-my/...`)
and rewrites the **replacement's** version. Putting the marker on that require instead would track
upstream's branch, which is never what anyone means by it. `listDirectDeps` excludes a require
covered by a tracked replace from the org auto-update path as well, for the same reason it excludes
a tracked require: the effective version is owned by branch tracking.

A replacement into a local directory (`=> ../foo`, `=> ./foo`, or an absolute path) has no remote
and carries no version, so there is no branch to resolve. A marker on one is skipped with a
warning.

### Pseudo-version majors

The pseudo-version is built for the module path being resolved, so a `/vN` path suffix (or
gopkg.in's `.vN`) produces a matching `vN.0.0-<timestamp>-<hash>`. A v0 pseudo-version on a post-v0
path is not a valid version: the go command rejects it with
`go.mod has post-v0 module path "..." at revision ...`.
