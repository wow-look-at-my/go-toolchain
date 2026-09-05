# Module utilities (`src/gomod/`)

Shared Go module utilities: module path reading, main package discovery.

## The root is an argument, never the working directory

`ReadModulePath`, `FindMainPackages` and `FindMainPackagesForTarget` each take the module root to read. Production passes `"."`, so the behaviour is what it always was. A test passes its own temp directory and never calls `os.Chdir`. That matters because the gosmopolitan fork runs tests in parallel unless one takes the serial barrier. A test that changed the process directory moved it under every test beside it, which showed up.

## Main package discovery

`FindMainPackages` → `hasMainPackage` → `packageNameFromFile` walks the module for non-test `.go` files declaring `package main`. The package clause is read with `go/parser` in `PackageClauseOnly` mode (the old hand-rolled line scanner skipped only the first line of a multi-line `/* */` license header, so a k8s-style copyright block hid the package clause and the main package was silently not built).

Without this gate an `ignore`-tagged `package main` generator sitting next to a real `package bench`/`package e2e` will be miscounted as a main package.

## Nested modules

`IsNestedModule` (a non-root dir containing its own go.mod) is the shared predicate every filesystem walker uses to skip nested modules.
