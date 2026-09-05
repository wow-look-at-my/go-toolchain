# Dependency handling

Depth for the "Dependency checking" line in the [README](../README.md#features) and step 3 of [the pipeline](PIPELINE.md). Four independent mechanisms run early in every pipeline, all before `go mod tidy`. Each rewrites `go.mod` in place, so the same rules as any other pipeline mutation apply. Locally you see the diff and commit it, in CI a resulting dirty tree fails the build (`checkDirtyInCI`) with an actionable message.

## v0.0.0 repair (`FixBogusDepsVersions`, `src/cmd/depsfix.go`)

A `require` line with version `v0.0.0` is not a valid, fetchable pseudo-version — it shows up when a dependency is added by hand (an editor auto-import, a copy-pasted line) instead. Before `go mod tidy` runs (which will otherwise just fail on it), go-toolchain resolves the module's default branch HEAD via `git ls-remote <url> HEAD` and rewrites the line to a real pseudo-version (`vX.Y.Z-<timestamp>-<12-hex-hash>`).

## Same-org auto-update (`src/cmd/deps.go`, `src/cmd/depsreport.go`)

Every run asynchronously checks each direct dependency's pseudo-version against `$GOPROXY/<module>/@latest`. For git-based (untagged) dependencies this proxy endpoint always resolves to the module's **default branch**. A module that carries *any* semver tag resolves to the highest such tag instead. Branch tracking (below) is the supported way off that path. Any outdated dependency sharing the current module's `host/org/` prefix (its own org — "trusted") is auto-updated in place via `go get -u` + `go mod tidy`. Anything else is only reported, with a `go get -u` hint.

## Branch-tracked dependencies (`src/cmd/depsbranch.go`)

Colloquially "the `//branch` comment" — spelled `go-toolchain:auto-branch` in the file, so searching a checkout for `//branch` finds nothing. Search for `auto-branch` or read on.

The two mechanisms above always resolve to a module's *default* branch — there is no way to ask either one to follow a different branch. A `require` line opts out of both and into branch tracking with a trailing comment:

```go
require github.com/wow-look-at-my/foo v0.0.0-20240101120000-abc123def456 // go-toolchain:auto-branch=v1
```

**The marker names no branch by default.** A branch's name lives on the remote, and a copy of it in `go.mod` is one more thing that goes stale. `auto-branch=<name>` names a different branch deliberately. And that is the only form that hardcodes anything.

**A bare marker follows the dependency's branch of *this repository's* name, when it has one, and its default branch otherwise** (`src/cmd/depsmatch.go`). Two repositories developed in tandem carry the same branch name: the change spans both, and neither half is finished without the other. So on the feature branch each side builds against the other's feature branch. That now carries the same code. Nothing was written down. So nothing has to be repointed — and nothing is left pointing at a branch that no longer exists.

Three things all mean "follow the default branch". This repository is on a detached HEAD (or is not a repository). The dependency has no branch of that name, or the dependency's own HEAD already points at it. The matching branch and the default branch are one `ls-remote`, not two round trips. And the answer is cached per module. A `go.mod` with no branch-tracked line asks nothing at all.

A matched branch is a *resolution*, never a rewrite: it is never written into `go.mod`. Writing it there will leave behind exactly the pin at a soon-deleted branch that the matching exists to avoid. `auto-branch=<name>` is never matched against — it says which branch. And that answer must not depend on where the reader is standing.

**There is one marker. And it is the whole vocabulary.** A line either follows a branch or it does not, and the only thing it ever names is a branch that is not the default:

```go
require github.com/wow-look-at-my/foo v0.0.0-... // go-toolchain:auto-branch
require github.com/wow-look-at-my/bar v0.0.0-... // go-toolchain:auto-branch=v1
```

### The legacy spelling migrates itself

The original `branch=<name>` spelling is still READ, so an unmigrated `go.mod` resolves correctly, and `markBranchTracked` rewrites it the first time it sees it. Nothing has to be migrated by hand.

The legacy spelling always names a branch, so the migration asks the remote one question: is that name merely the default branch? A name that repeats the default is DROPPED — `branch=master` on a repository whose default is `master` becomes plain. A name that does not is KEPT: `branch=v1` becomes `auto-branch=v1`, because that was a deliberate choice. A remote that cannot answer keeps the name and warns. So the migration is never a change of meaning.

**Changing the marker's spelling again takes two releases, in this order: read it first, write it one release later.** A binary predating a marker does not ignore one it fails to recognize. So one committed `go.mod` has to satisfy the whole fleet at once, and with a single marker per line no text does until every runner.

Every run re-resolves that branch's current HEAD via `git ls-remote <url> refs/heads/<branch>` and rewrites the pseudo-version in place (the comment is preserved. So the pin stays declarative across runs). `listDirectDeps` excludes any such line from the org auto-update path so the two mechanisms never fight over the same require. The dependency still always resolves to one concrete, `go.sum`-verified pseudo-version — go-toolchain does not do unpinned/floating dependency resolution.

**The recorded version is a cache of the last resolution, not a contract.** `branch=master` says the branch is what this dependency means. Every run re-answers it. So the rewrite is not a change anybody has to sign off on, and `checkDirtyInCI` excludes it. A commit whose whole content is a hash nobody chose is noise, and demanding one will make the marker mean a bump commit per upstream. The exclusion is exactly the version token on a line carrying the same marker in `HEAD` and the working tree. Anything else — a require added or dropped, a marker that appeared, an edited comment, a `go.sum` line for a module that did not move.

Both direct and indirect requires are tracked. An UNMARKED indirect line is somebody else's answer -- what `go mod tidy` computed from the rest of the module graph -- and carries no anchor of its own. But a marked indirect line resolves exactly like a marked direct one. An org dependency with no direct require of its own (`github.com/wow-look-at-my/yaml-fixed`, reached only as a transitive dependency of `go-git`, is the case that motivated this) has no other line to ride along with. So its own marker is the only way to track it. A same-repository sibling (below) works either way. Whether `go mod tidy` marked the line indirect on its own, or the line was already independently tracked. The line keeps being resolved.

### Multi-module repositories resolve as a unit (`src/cmd/depssiblings.go`)

A repository cut into several modules cannot pin itself. `go/client` requires `go/core` at a pseudo-version, and that line was written *before* the commit publishing them both existed. So it necessarily names an earlier one. At the repository's first publish it names one with no such module in it at all. And a consumer gets. A relative `replace` hides it inside the repository and nowhere else, because a replace is main-module-only.

So a tracked module brings its siblings with it. Resolving one reads its `go.mod` at the commit it resolved to, walks the requires that live in the same repository.

```go
require github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260815165120-e431c66a9f25 // go-toolchain:auto-branch
require github.com/wow-look-at-my/common-ai-api/go/core v0.0.0-20260815165120-e431c66a9f25 // go-toolchain:auto-branch
```

Minimal version selection then takes the newer of the two answers for `go/core`, so the stale pin inside `go/client` loses and is never fetched. One repository, one commit, whatever the modules inside it say about each other.

**Nothing declares which modules share a repository, because the repository already knows.** The added line carries the same ordinary marker as the line that brought it in, and cohesion comes from `repoResolver` (`depsbranch.go`) instead. It answers each repository ONCE, keyed on the repository root it discovers plus the branch being followed. Two modules resolved a moment apart therefore cannot land on different commits. And a `go.mod` never carries a claim about repository membership that can go.

The walk is over requirements, not over the repository, so only modules the tracked one actually needs come along. A sibling missing at the resolved commit FAILS the run. Writing a pin to a commit that does not carry the module is the failure this exists to prevent, not something to skip past.

### A tracked branch with an open pull request (`src/cmd/depsbranchguard.go`)

A branch that is the head of an open pull request has a scheduled death: the merge that closes the PR deletes it. Point a pin at one and it resolves fine, CI goes green, the change merges — and the branch is gone.

So a marker naming an explicit branch is checked against the open pull requests of the repository it belongs to. In CI this FAILS, because CI is the last look at a change before it merges and green there is what the merge is decided. Locally it only warns. Developing two repos in tandem, pointed at each other's unmerged branches, is a real workflow.

A bare `auto-branch` is never checked. It *can* match a branch with an open pull request — that is the tandem workflow above — but it wrote nothing down. There is nothing to repoint, so there is nothing to warn about.

The check needs the GitHub API. And it answers "cannot tell" as no finding plus a warning. A guard that turned an unreachable API into a failed build will fail runs over the network rather than over the thing it checks. A private repository needs `GITHUB_TOKEN` or `GH_TOKEN` in the environment. Without one the warning says so.

### Tracking a fork through a `replace`

A fork keeps upstream's module path — that is what makes it a drop-in — so it is consumed. The marker therefore goes on the replace line, where the version that actually reaches the build lives:

```go
require charm.land/bubbletea/v2 v2.0.8

replace charm.land/bubbletea/v2 => github.com/wow-look-at-my/bubbletea/v2 v2.0.0-20260812203640-351d2159f8d8 // go-toolchain:auto-branch
```

The branch is resolved against the **replacement's** repository (`github.com/wow-look-at-my/...`) and rewrites the **replacement's** version. Putting the marker on that require instead will track upstream's branch, which is never what anyone means by it. `listDirectDeps` excludes a require covered by a tracked replace from the org auto-update path as well, for the same reason it excludes a tracked require. The effective version is owned by branch tracking.

A replacement into a local directory (`=> ../foo`, `=> ./foo`, or an absolute path) has no remote and carries no version, so there is no branch to resolve. A marker on one is skipped with a warning.

### Pseudo-version majors

The pseudo-version is built for the module path being resolved, so a `/vN` path suffix (or gopkg.in's `.vN`) produces a matching `vN.0.0-<timestamp>-<hash>`. A v0 pseudo-version on a post-v0 path is not a valid version: the go command rejects it with `go.mod has post-v0 module path "..." at revision ...`.

## Branch tracking is mandatory for org modules (`src/cmd/depsbranchenforce.go`)

A version pin on a `github.com/wow-look-at-my/` module is a snapshot of whenever someone last ran `go get`. These modules are co-developed with their consumers and have no release cadence to pin to. So nothing ever moves that snapshot forward and the consumer silently builds against month-old code. There is no exception for staleness here. An org module tracks a branch whether it is a direct or an indirect require, with no version-pin opt-out. The branch pin is the canonical form for them, and `go.mod` is rewritten into it. An org require or replace carrying a plain version gets the bare `// go-toolchain:auto-branch` comment appended, direct or indirect. Appending it costs no lookup — it names no branch, so there is nothing to ask until the line is resolved.

A line already carrying the canonical marker is left exactly as it is. The legacy spelling is migrated, which is the one place this asks the remote anything (see above).

The rewrite is the enforcement — locally you review the diff and commit. A marker appearing is content, not a resolution, so the pin-movement exclusion above does not cover it. The up-to-date fast exit consults `untrackedOrgDeps` (a `go.mod` parse, no network) as well, because an unchanged tree can predate this requirement and the run being skipped.

One shape is not rewritten. And it is not silently skipped either: **a require whose version a `replace` overrides.** Nothing is lost here: the replacement supplies the version that reaches the build. And the replace line is marked in the require's place.

**Only a `replace` that NAMES A VERSION overrides anything.** A `replace` into a local directory (`=> ../reader`) is main-module-only: it points *this* repository at a tree on disk and tells a consumer nothing at all. Every consumer resolves the `require`'s own version, so that require is still marked and still tracked — the directory replace beside it stays bare.

Treating a locally-replaced require as covered was a real hole, and the multi-module case above is where it bit. `validator` required its sibling `reader` at a pseudo-version, `replace ../reader` hid it from this repository's own builds, and nothing ever moved it. The pin ended up naming a commit older than `reader/go.mod` itself, so every CI run here was green while every consumer got `missing go.mod at revision`.

There is no version-pin opt-out. A stale org dependency, direct or indirect, is a build the consumer no longer gets to make.
