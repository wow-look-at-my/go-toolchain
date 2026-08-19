# cosmocompat: patching third-party cosmo gaps for every consumer

`src/cosmocompat` closes a `GOOS=cosmo` build gap in a THIRD-PARTY module —
one gosmopolitan has no upstream port for — for ANY consumer repo, not just
the one that first hit it. Before this package existed, a repo that hit the
gap (`github-state-mirror`, depending on `modernc.org/sqlite`) carried its own
per-repo `cosmopatch/` generator: a `go run` step in that repo's own CI, a
`go.mod replace` block pointing at its output, and a README explaining how to
run it. That fixed one repo. The next repo to depend on `modernc.org/sqlite`,
or on `modernc.org/libc` or `golang.org/x/sys` directly, would have had to
reproduce all of it from scratch. cosmocompat exists so no repo ever has to.

## What it does, and when

`matrix`'s cosmo build calls `cosmocompat.Prepare(".")` once the fork
toolchain is confirmed available (`runReleaseWithRunner` in
`src/cmd/matrixrun.go`), never before — a genuinely missing toolchain must
still fail with THAT error, not a confusing go.mod-parsing error from this
step. `Prepare` reads the consumer's own `go.mod`, and for each module in
`knownGaps` (`src/cosmocompat/gaps.go`) that the consumer actually depends on
— checked against `go.mod`'s `require` block, not a `go list` round trip —
it:

1. Downloads that exact module version with `go mod download -json`, run
   with `cmd.Dir` set to the CONSUMER's own directory, so it resolves through
   the consumer's own `GOPROXY`/auth configuration.
2. Copies the downloaded module tree into a scratch directory.
3. Applies the gap's `copies` (an existing platform file duplicated to a
   `_cosmo` sibling with an explicit `//go:build cosmo` tag — the standard
   library's implicit filename-OS convention does not recognize `cosmo`),
   `tagEdits` (excluding an existing file from cosmo builds by ANDing
   `!cosmo` into its build tag), and any embedded `overlay` files (a small
   hand-written cosmo implementation, for a case no existing file can be
   copied from) or `postPatch` step (an in-place source edit, for a
   `//go:linkname` pragma mismatch under the fork).
4. Returns a generated `go.work` file whose `replace` directives point each
   patched module at its scratch copy, plus a `use` of the consumer's own
   directory.

The build sets `GOWORK` to that path for the cosmo build invocation only
(`matrixbuild.go`'s `runBuild`); every other phase (tests, vet, non-cosmo
targets) is unaffected, because `GOWORK` is never set for them.

## Zero-cost no-op for everyone else

A consumer that depends on none of `knownGaps` gets an empty `gaps` slice from
`Prepare`, which returns `("", func(){}, nil)` immediately — no network call,
no scratch directory, no `GOWORK`. This is the common case: cosmocompat is a
no-op for every repo except the handful that actually need it.

## The consumer's own `go.mod` and `go.sum` are never touched

Everything cosmocompat does lives in a temporary scratch directory
(`os.MkdirTemp`), cleaned up via the `cleanup` func `Prepare` returns
(`defer cosmoCleanup()` in `matrixrun.go`). The consumer repo needs no
`cosmopatch/` directory, no `go.mod replace` block, and no CI step of its
own — this is the whole point: the fix lives once, here, and applies to every
consumer automatically.

## A consumer's own `replace` wins

`neededGaps` skips any module the consumer's `go.mod` already replaces itself
— cosmocompat never overrides a consumer's own intentional replace. A module
the consumer does not depend on at all is skipped too (not in `require`).

## Known gaps

`src/cosmocompat/tables_libc.go`, `tables_xsys.go`, `tables_sqlite.go` declare
the three gaps currently known: `modernc.org/libc`, `golang.org/x/sys`, and
`modernc.org/sqlite`. Each entry pins a `verifiedVersion` — the version the
patch set was last proven against. cosmocompat applies its patches to
whatever version the CONSUMER pins (not the verified version), because a
consumer's own `go.mod` is the source of truth for what actually builds; the
verified version is a note for whoever next updates the patch set, not an
enforced floor or ceiling.

## Adding a new gap

Add an entry to `knownGaps` in `gaps.go` (module path, verified version,
`copies`/`tagEdits`/`overlays`/`postPatch` as needed) in a new
`tables_<name>.go` file, following the shape of the three existing files. An
overlay source file goes under `src/cosmocompat/overlay/` with a `.tmpl`
extension — NOT `.go` — so go-toolchain's own build, vet, and file-length
checks never touch it (it belongs to a different package/module, `unix` or
`libc`, and would fail to compile as part of go-toolchain's own tree).

## Why this lives in go-toolchain, not gosmopolitan or the consuming repo

The gap is in third-party modules gosmopolitan does not own — forking
`modernc.org/libc`, `golang.org/x/sys`, or gosmopolitan itself to carry these
patches upstream needs each project's own maintainers, or an explicitly
authorized fork. go-toolchain is the one piece of infrastructure every cosmo
build in the org already runs through, so it is the one place a fix here
reaches every consumer for free.
