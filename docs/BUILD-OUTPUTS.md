# Build output lifetime

A binary in `build/` means one thing: the run that produced it succeeded. To
keep that true, go-toolchain deletes the artifacts of its own build targets

- **before the pipeline starts**, so a failure at any phase — or a crash, or a
  kill — leaves nothing runnable behind;
- **when the run fails after the build phase already wrote them** (a red dats
  suite, the coverage or warnings gate);
- **when the agent output guard refuses to run**, or when the Go bootstrap
  fails before the pipeline is reached.

Only the target's own artifacts are touched: the bare name (`<name>.exe` and
the fat APE), every `<name>_…` shape the toolchain writes
(`<name>_<goos>_<goarch>`, the wasm names, the `<name>_host` symlink), and the
APE's `<name>.…` sidecar ELFs. `checksums.txt`,
`wasm_exec.js`, `profile.json` and anything else in `build/` are left alone.

The target file is never written directly. The compiler's `-o` is
`build/.tmp-<name>` (hidden on Windows), and only a build that succeeded moves
the result — plus any sidecar the cosmo APE build names after its `-o` path —
onto `build/<name>`. A failing build deletes what it wrote; a build killed
before it could commit is swept on the next delete, because the `.tmp-`
spelling of every artifact shape above counts as an artifact too. So
`build/<name>` appears whole or not at all, and only when a build actually
finished — a half-written binary cannot exist there to be mistaken for a
result.

The point is that a hidden failure cannot be laundered into a success by
running a leftover binary: with the output discarded and the exit code ignored,
a stale `build/<target>` is the last thing that can pass for a build that never
happened, so it does not survive. There is deliberately no flag or environment
variable to disable this. `⇒ Up to date, nothing to do` is unaffected — that
fast exit means the last run succeeded and its outputs are intact.

