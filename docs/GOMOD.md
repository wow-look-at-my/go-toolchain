# Module utilities (`src/gomod/`)

Shared Go module utilities: module path reading, main package discovery.

## The root is an argument, never the working directory

`ReadModulePath`, `FindMainPackages` and `FindMainPackagesForTarget` each take the module root to read. Production passes `"."`, so the behaviour is
what it always was; a test passes its own temp directory and never calls `os.Chdir`. That matters because the gosmopolitan fork runs tests in
parallel unless one takes the serial barrier. A test that changed the process directory moved it under every test beside it, which showed up as
`getwd: no such file or directory`, as one test's fixtures landing inside another's temp tree, and as fixture directories left behind in
`src/gomod` itself.

## Main package discovery

`FindMainPackages` → `hasMainPackage` → `packageNameFromFile` walks the module for non-test `.go` files declaring `package main`; the package clause
is read with `go/parser` in `PackageClauseOnly` mode (the old hand-rolled line scanner skipped only the first line of a multi-line `/* */` license
header, so a k8s-style copyright block hid the package clause and the main package was silently not built), but **honors build constraints** first:
`fileMatchesBuild` calls `go/build`'s `build.Default.MatchFile(dir, name)` to skip any file excluded from the build for the current context —
notably the `//go:build ignore` / `// +build ignore` generator idiom (`//go:build ignore` + `package main`, run via `go run file.go`), plus
GOOS/GOARCH filename/tag mismatches.

Without this gate an `ignore`-tagged `package main` generator sitting next to a real `package bench`/`package e2e` would be miscounted as a main
package, and memlimit would inject a non-ignored `package main` guard into that dir, breaking the cross-compile with `found packages bench
(bench_test.go) and main (gomemlimit_gen.go)`. A legitimately-constrained main (e.g. a `package main` under `//go:build linux`) is still
discoverable under the matching context — only build-excluded files are dropped.

## Nested modules

`IsNestedModule` (a non-root dir containing its own go.mod) is the shared predicate every filesystem walker uses to skip nested modules —
`FindMainPackages`, test-package discovery (`listTestPackages` in `src/test/test.go`), the coverable-statements walk (`HasCoverableStatements` in
`src/test/coverable.go`), build-target discovery's library-only fallback (`findAllPackagesByDir` in `src/build`), the vet fixers (gofmt,
testify/gotest.tools import migrations, unused-range-vars), and the file-length check all skip any nested module, whose files belong to their own
module and must stay byte-identical to upstream (a nested module's packages are not import paths of the outer module, so listing them fails `go
test`/`go build` with `no required module provides package ...`).
