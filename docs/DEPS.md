# Dependency handling

Depth for the "Dependency checking" line in the [README](../README.md#features) and step 3 of
[How It Works](../README.md#how-it-works). Four independent mechanisms run early in every
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

**The marker names no branch by default.** `// go-toolchain:auto-branch` follows the module's
*default* branch, asked of the remote on every run. A branch's name lives on the remote, and a copy
of it in `go.mod` is one more thing that goes stale — the day a default branch is renamed, every
hardcoded copy across the org resolves to nothing. `auto-branch=<name>` names a different branch
deliberately, and that is the only form that hardcodes anything.

The original `branch=<name>` spelling is still READ, and every marker is WRITTEN with a
compatibility half behind it:

```go
require github.com/wow-look-at-my/foo v0.0.0-... // go-toolchain:auto-branch go-toolchain:branch=master
require github.com/wow-look-at-my/bar v0.0.0-... // go-toolchain:auto-branch=v1 go-toolchain:branch=v1
require github.com/wow-look-at-my/baz v0.0.0-... // go-toolchain:sibling=github.com/org/repo/go/client go-toolchain:branch=master
```

A go-toolchain release that predates these markers looks for `go-toolchain:branch=` and takes
EVERYTHING after it as the branch name. So the legacy spelling goes LAST, with nothing after it,
and both readers answer correctly off one line: an old release follows the named branch, a current
one follows the marker in front and ignores the rest. Neither rewrites the other's work.

That half is what makes the new markers shippable at all. An old release does not IGNORE a marker
it does not recognize — it reads the line as untracked and appends its own comment, on a line of
its own, ABOVE the require, which corrupts the block. So the migration is automatic and total:
`EnforceOrgBranchTracking` brings every unmarked line and every legacy line to the bridged form on
the next run, and a repo whose builds still run an older release keeps working throughout.

The compatibility half names the branch the line ACTUALLY follows, never the default one:
`branch=v1` on a repo whose default is `master` becomes `auto-branch=v1 go-toolchain:branch=v1`.
Naming the default there would quietly move a deliberate non-default pin onto master for every old
reader. Where the name only repeated the default, the new half stops naming it: `branch=master`
becomes plain `auto-branch`, which stops caring what the branch is called.

Writing a marker therefore needs the default branch, which costs one
`git ls-remote --symref <url> HEAD` — the same lookup the resolution had to make anyway. A remote
that cannot answer is FATAL: a marker written without its compatibility half is one an older
release corrupts on sight, and reporting green over a marker that never got written is the other
way to be wrong here.

The half is redundant the moment every runner reads the marker in front of it, and a later release
drops it.

Every run re-resolves that branch's current HEAD via `git ls-remote <url> refs/heads/<branch>` and
rewrites the pseudo-version in place (the comment is preserved, so the pin stays declarative across
runs); `listDirectDeps` excludes any such line from the org auto-update path so the two mechanisms
never fight over the same require. The dependency still always resolves to one concrete,
`go.sum`-verified pseudo-version — go-toolchain does not do unpinned/floating dependency
resolution — the marker only tells it *which* branch's HEAD that pin should track.

**The recorded version is a cache of the last resolution, not a contract.** `branch=master` says
the branch is what this dependency means; every run re-answers it. So the rewrite is not a change
anybody has to sign off on, and `checkDirtyInCI` excludes it: a commit whose whole content is a
hash nobody chose is noise, and demanding one would make the marker mean a bump commit per
upstream push — the opposite of what it is for. The exclusion is exactly the version token on a
line carrying the same marker in `HEAD` and the working tree, plus the `go.sum` hashes for the
modules that moved. Anything else — a require added or dropped, a marker that appeared, an edited
comment, a `go.sum` line for a module that did not move — still fails the build as dirt.

Only direct (non-`// indirect`) requires are tracked; an indirect line carrying the marker is
skipped with a warning rather than silently ignored, since indirect dependencies are resolved
transitively and a per-line branch pin on one would not mean what it looks like it means. The
warning names the way that does work: a `replace` carrying the marker (see below) is main-module
only, so it applies to direct and indirect requires alike and pins the version that actually
reaches the build. A same-repository sibling (below) is the exception: that line is this run's own
and `go mod tidy` is what marked it indirect, so it keeps being resolved and draws no warning.

### Multi-module repositories resolve as a unit (`src/cmd/depssiblings.go`)

A repository cut into several modules cannot pin itself. `go/client` requires `go/core` at a
pseudo-version, and that line was written *before* the commit publishing them both existed, so it
necessarily names an earlier one. At the repository's first publish it names one with no such
module in it at all, and a consumer gets `missing go.mod at revision <hash>` — which is the whole
of that bug: not a wrong pin, but the only pin a commit can carry about itself. A relative
`replace` hides it inside the repository and nowhere else, because a replace is main-module-only.

So a tracked module brings its siblings with it. Resolving one reads its `go.mod` at the commit it
resolved to, walks the requires that live in the same repository, and requires each of them here
at that same commit, marked to keep tracking the same branch:

```go
require github.com/wow-look-at-my/common-ai-api/go/client v0.0.0-20260815165120-e431c66a9f25 // go-toolchain:auto-branch
require github.com/wow-look-at-my/common-ai-api/go/core v0.0.0-20260815165120-e431c66a9f25 // go-toolchain:sibling=github.com/wow-look-at-my/common-ai-api/go/client
```

Minimal version selection then takes the newer of the two answers for `go/core`, so the stale pin
inside `go/client` loses and is never fetched. One repository, one commit, whatever the modules
inside it say about each other.

The added line says `sibling=<module>`, not a branch, because that is what is true of it: it
matches whatever commit that module resolved to. Writing a branch there would be a coincidence
dressed as a declaration — correct only while the anchor happens to follow that branch's head. It
also would not survive the case below.

**A deliberately pinned module anchors its siblings too**, at the commit its own version names
rather than at a branch head. Cohesion is about the modules of one repository shipping together, so
a module held at an old version holds its siblings at that same old commit; following the branch
there would pair a pinned module with siblings from today, which is the mismatch the pin exists to
avoid. A `go-toolchain:pinned` line on a *sibling* still wins over cohesion, since moving with its
siblings is exactly what that marker opts out of.

The walk is over requirements, not over the repository, so only modules the tracked one actually
needs come along. A sibling missing at the resolved commit FAILS the run: writing a pin to a commit
that does not carry the module is the failure this exists to prevent, not something to skip past.

### A tracked branch with an open pull request (`src/cmd/depsbranchguard.go`)

A branch that is the head of an open pull request has a scheduled death: the merge that closes the
PR deletes it. Point a pin at one and it resolves fine, CI goes green, the change merges — and the
branch is gone, so the next run on the *default* branch resolves to nothing, after the thing that
broke it has already landed.

So a marker naming an explicit branch is checked against the open pull requests of the repository
it belongs to. In CI this FAILS, because CI is the last look at a change before it merges and green
there is what the merge is decided on. Locally it only warns: developing two repos in tandem,
pointed at each other's unmerged branches, is a real workflow, and the warning is the reminder to
repoint before the pull request goes up. A bare `auto-branch` is never checked — it names no
branch, so it cannot be pointed at a temporary one.

The check needs the GitHub API, and it answers "cannot tell" as no finding plus a warning: a guard
that turned an unreachable API into a failed build would fail runs over the network rather than
over the thing it checks. A private repository needs `GITHUB_TOKEN` or `GH_TOKEN` in the
environment; without one the warning says so.

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

## Branch tracking is mandatory for org modules (`src/cmd/depsbranchenforce.go`)

A version pin on a `github.com/wow-look-at-my/` module is a snapshot of whenever someone last
ran `go get`. These modules are co-developed with their consumers and have no release cadence to
pin to, so nothing ever moves that snapshot forward and the consumer silently builds against
month-old code. The branch pin is therefore the canonical form for them, and `go.mod` is
rewritten into it: an org require or replace carrying a plain version gets the bridged
`// go-toolchain:auto-branch go-toolchain:branch=<default>` appended, and a line still carrying
the legacy spelling is migrated to the same form. Both need the default branch, so both ask the
remote for it — see the compatibility half above for what that half is and why a failure to
resolve it is fatal.

A line already in the bridged form is left exactly as it is, and asks the remote nothing.

The rewrite is the enforcement — locally you review the diff and commit it, in CI the resulting
dirty tree fails the build (`checkDirtyInCI`), the same contract as every other `go.mod` mutation
here. A marker appearing is content, not a resolution, so the pin-movement exclusion above does not
cover it. The up-to-date fast exit consults `untrackedOrgDeps` (a `go.mod` parse, no network) as
well, because an unchanged tree can predate this requirement and the run being skipped is the one
that would fix it.

Two shapes cannot be rewritten. Neither is silently skipped:

- **Indirect requires.** Branch tracking skips indirect lines, so a marker written there would
  read like a pin and track nothing — the rewrite would be a lie. The module is still
  version-pinned, so it WARNS instead, naming both repairs: promote it to a direct require, or
  pin the version that reaches the build with a tracked `replace` (main-module-only, so it covers
  indirect requires too). Both change what the build resolves, which is why neither is applied for
  you. A tracked replace already covering the module, or the `pinned` opt-out, silences it.
- **A require overridden by a `replace`.** Nothing is lost here: the replacement supplies the
  version that reaches the build, and the replace line is marked in the require's place. A
  replacement into a local directory has no remote to resolve and is left alone.

Genuinely wanting a version pin — a tagged release with a hard API break past it, say — is an
explicit opt-out on the line, with the reason next to it:

```go
require github.com/wow-look-at-my/foo v1.2.3 // go-toolchain:pinned v2 is a hard API break
```
