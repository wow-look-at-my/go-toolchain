# The `go` directive in go.mod, and why it is load-bearing twice

`go.mod`'s `go` directive normally sets a language version. Here it also
decides two things that reach outside this module, and getting either wrong
produces a failure that does not mention a version anywhere.

## 1. It picks the Go that BUILDS this binary, and vet cannot read newer export data

`go-bootstrap` (src/cmd/gobootstrap.go) compares the installed Go against this
directive: an installed Go at or above it is used as-is, an older one is
replaced with a download. CI does the same through
`actions/setup-go`'s `go-version-file: 'go.mod'`. So the directive is the Go
that compiles the released binary.

That matters because the vet phase type-checks with the `go/types` LINKED INTO
this binary, while the packages it reads were compiled by whatever Go the
consumer is building with. Export data is forward-compatible and not
backward-compatible: a newer compiler writes constructs an older `go/types` has
no representation for. When the two drift apart, vet fails on code that is
perfectly fine.

Go 1.27 is the first release to make this bite. It allows generic METHODS, and
`math/rand/v2` immediately uses one:

```go
func (r *Rand) N[Int intType](n Int) Int
```

`go/types.NewSignatureType` panicked on a signature carrying both a receiver
and type parameters in every release up to 1.26. x/tools recovers that panic
and reports it as an importer error, so a Go 1.25-built go-toolchain analyzing
a Go 1.27 build says:

```
internal error in importing "math/rand/v2" (function with type parameters
cannot have a receiver); please report an issue
```

followed by a cascade of undefined symbols in a package nobody touched. Nothing
in that names a Go version, which is why `src/cmd/staleanalyzer.go` recognizes
the signature and reports the skew with both halves named. That diagnostic is a
fallback, not the fix: the fix is that the directive keeps this binary built
with a current Go.

**So: when a new Go minor ships, bump this directive.** The alternative is that
released binaries silently stop being able to analyze what people build.

## 2. It must stay a BARE MINOR, because of the cosmo fork

Write `go 1.27`. Never `go 1.27.0`.

The gosmopolitan fork stamps a version (`go1.27.0cosmo`) that does not parse as
a release version, so it self-identifies as the DEV version `1.27`. A dev
version satisfies `go 1.27` and does NOT satisfy `go 1.27.0`. With a
release-shaped directive the fork refuses the module outright:

```
go: go.mod requires go >= 1.27.0 (running go 1.27; GOTOOLCHAIN=local)
```

which kills the `matrix` phase — this repo builds its own APE with that fork.
`normalizeGoVersion` in gobootstrap.go already appends the `.0` that the
download URL needs, so the bare minor costs nothing on the bootstrap side.

## Checklist for a Go minor bump

1. Set the directive to the bare minor (`go 1.<N>`).
2. Rebuild go-toolchain with that Go and run the full pipeline — the vet phase
   is what exercises the export-data path.
3. Run `matrix` against the cosmo fork (`GO_TOOLCHAIN_COSMO_GOROOT` points at a
   local gosmopolitan build) to confirm the fork still accepts the directive.
4. If a NEW type-system construct breaks the analyzer in a way the current
   markers do not catch, add its message to `staleAnalyzerMarkers`.
