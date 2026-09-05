# Automatic GOMEMLIMIT (cgroup-aware memory limit)

By default, go-toolchain injects a small, stdlib-only startup guard
(`gomemlimit_gen.go`) into every `main` package it builds. Main-package
discovery honors build constraints, so a `//go:build ignore` `package main`
generator file (the common `go run gen.go` idiom) sitting next to a real
non-main package is correctly skipped. When the resulting
binary starts, the guard reads the container's cgroup memory limit (cgroup v2 or
v1) and calls `runtime/debug.SetMemoryLimit` with 90% of it. This keeps the Go
garbage collector under the cgroup ceiling — as the heap approaches the limit
the GC works harder, trading CPU for memory.

The guard is a **transient build artifact**: go-toolchain writes it into each
`main` package immediately before compiling and removes it again as soon. So it never lingers in your working tree and never needs to be
committed. The CI dirty-tree check ignores `gomemlimit_gen.go` in every git
state — added, modified, or deleted. Before injecting,
go-toolchain also lists `gomemlimit_gen.go` in the repository's clone-local
`.git/info/exclude` (idempotently; the entry stays), so the guard is invisible
to `git status` for the whole build window. The exclude file lives under `.git/`,
outside your working tree, so the entry itself can never show up as a change
(which is exactly why the guard is not added to `.gitignore` instead). The
guard is dependency-free (no `go.mod`/`go.sum` changes), carries the standard
`// Code generated ... DO NOT EDIT.` marker (so it is excluded from coverage),
is idempotent, and is a no-op when no cgroup limit is found.

If a repository committed the guard under an older go-toolchain, the cleanup
deletes those files from the working tree on the next run (without failing the
build). Commit that deletion once to drop the stale files for good.

Injection is unconditional — there is no build-time flag or environment variable
to turn it off. Opting out is a run-time decision instead, via the variables
below, which is the layer that actually knows whether a given deployment wants
the cap.

The following are read by the built program **when it starts**, not at build time:

```bash
# Opt a single deployment out without rebuilding — Go's own variable wins
export GOMEMLIMIT=off

# ...or pin an explicit limit (the guard then does nothing)
export GOMEMLIMIT=2GiB

# Tune the headroom ratio (default 0.9); "off" also disables the guard
export GO_TOOLCHAIN_MEMLIMIT_RATIO=0.8
```

