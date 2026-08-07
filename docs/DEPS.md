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
transitively and a per-line branch pin on one would not mean what it looks like it means.
