# The revision stamp

Every build this pipeline runs passes `-ldflags` on the go command line. That
string is assembled in `jobLDFlags` (`src/cmd/forkbuild.go`) and consumed by
`runBuild` (`src/cmd/matrixbuild.go`), in this order:

```
<revision stamp>  <caller's GOFLAGS -ldflags>  -buildid=
```

Each part is there for a different reason, and the order is what makes them
coexist.

## Why the caller's GOFLAGS had to be folded in

`GOFLAGS=-ldflags=-X=main.gitHash=$SHA` is the documented way to stamp a
binary without editing the build command. It did not work here, and it failed
silently.

The go command applies GOFLAGS to its flag set BEFORE it parses argv
(`cmd/go/main.go` calls `base.SetFromGOFLAGS` and then `cmd.Flag.Parse`). So a
command-line `-ldflags` REPLACES the GOFLAGS spelling rather than adding to it.
This pipeline always passes `-ldflags`, so the caller's value was discarded on
every build, with no message. The binary shipped carrying its placeholder and
reported itself a development build wherever it ran.

`callerLDFlags` (`src/cmd/callerldflags.go`) reads `GOFLAGS` and folds every
`-ldflags` it finds into the composed value. It splits the variable the way the
go command splits it (`cmd/internal/quoted.Split`): on whitespace, with `''` or
`""` around a WHOLE field. A quote opening anywhere but at a field's start is
ordinary text, so

- `GOFLAGS=-ldflags="-X a=b"` does NOT survive as a field, and
- `GOFLAGS="'-ldflags=-X a=b'"` does.

An unterminated quote yields nothing, matching the go command's refusal of the
whole value.

## Why the stamp exists at all

Go stamps `vcs.revision` into a binary by itself, and `debug.ReadBuildInfo`
reads it back. That is automatic and needs no help — as long as the build
directory is inside a git work tree.

A container build usually is not. `COPY . .` with `.git` in `.dockerignore`
hands the builder a source tree and no history. So the go command's stamping
finds nothing, `vcs.revision` is absent, and every binary the image carries
reports an unknown commit. Nothing said so.

`stampLDFlags` (`src/cmd/vcsstamp.go`) fills that gap. It resolves the commit,
then writes an `-X` for each stamp variable the main package actually declares.

## Which variables get filled

`stampVarNames` is the table, tried in this order against what the package
declares:

`gitHash`, `GitHash`, `gitCommit`, `GitCommit`, `commit`, `Commit`,
`revision`, `Revision`

A package that declares none of them gets no stamp and no warning: it never
asked for one. Declaring `var gitHash string` in `package main` is the whole
opt-in — there is no flag, no input, and no configuration file.

Discovery is `gomod.PackageStringVars`, which parses the package's non-test
files and reports only the package-level variables it can PROVE hold strings.
An explicit `string` type, or an initializer that is a string literal. That
narrowness is not fussiness. `cmd/link`'s `addstrdata` returns silently for a
symbol it cannot find, but calls `Errorf` for a symbol that is not a string
variable. Stamping only
what the source proves is a string is what keeps this from breaking a consumer
that happens to reuse a name.

## Where the revision comes from

In order:

1. `GO_TOOLCHAIN_VCS_REVISION`
2. `git rev-parse HEAD`
3. `GITHUB_SHA`

The explicit variable leads because it answers the case the others cannot. A
build whose tree has no history, where git has nothing to report and the CI
variable is not in the container either. A Dockerfile passes it through:

```dockerfile
ARG GIT_HASH
RUN GO_TOOLCHAIN_VCS_REVISION="${GIT_HASH}" go-toolchain
```

A revision holding whitespace or a quote is rejected (`usableRevision`) rather
than passed through. The go command re-splits the `-ldflags` value, so such a
revision would silently become extra flags.

When a package declares a stamp variable and NO source names a revision, the
build warns and leaves the variable alone. The binary then ships its
placeholder, which is exactly the state that used to pass unnoticed.

## Why the caller trails the stamp

`cmd/link` keeps the LAST `-X` given for a name (`addstrdata1` writes into
`strdata` per name, so a later occurrence overwrites an earlier one). The stamp
leads and the caller's own flags trail it, so an explicit `-X` from GOFLAGS
overrides the resolved revision. `-buildid=` stays last of all, which is a
different flag and a different rule. The go command takes the final `-ldflags`
string as a whole, and the reproducibility flag has to be in it (see
[MATRIX.md](MATRIX.md)).
