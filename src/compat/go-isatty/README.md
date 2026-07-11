# Local copy of github.com/mattn/go-isatty (v0.0.20)

This directory is a nested Go module that the root `go.mod` substitutes for
the upstream module:

```
replace github.com/mattn/go-isatty => ./src/compat/go-isatty
```

## Why it exists

go-toolchain builds as a `GOOS=cosmo` fat APE (Actually Portable Executable)
with the gosmopolitan Go fork. Upstream go-isatty gates every implementation
file on explicit GOOS lists, none of which know `cosmo`, so the package
compiles to an **empty** package under cosmo and every importer fails with
`undefined: isatty.IsTerminal`. The import chain is unavoidable:
`src/test` -> `gotest.tools/gotestsum/testjson` / `bitfield/gotestdox` ->
`fatih/color` -> `mattn/go-isatty`.

The copy is byte-identical to upstream v0.0.20 (source files, `LICENSE`,
`go.mod`) **plus one added file**: `isatty_cosmo.go` (`//go:build cosmo`),
which approximates `IsTerminal` via `syscall.Fstat` + `S_IFCHR` and reports
`IsCygwinTerminal` as false. Non-cosmo builds compile the exact upstream
files, so host behavior is unchanged.

## Maintenance

- When bumping the go-isatty version in the root `go.mod`, re-copy the
  upstream sources here (keep them byte-identical) and re-add
  `isatty_cosmo.go`.
- Delete this directory and the `replace` once upstream ships cosmo support
  (or the fork grows an x/sys/unix port that makes the tcgets path work).
- Upstream: https://github.com/mattn/go-isatty (MIT, see `LICENSE`).
