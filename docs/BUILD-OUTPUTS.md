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
`build/.tmp-<name>`, and only a build that succeeded moves the result. A failing build deletes what it wrote; a build killed
before it could commit is swept on the next delete. So
`build/<name>` appears whole or not at all, and only when a build actually
finished.

The point is that a hidden failure cannot be laundered into a success by
running a leftover binary. With the output discarded and the exit code ignored,
a stale `build/<target>` is the last thing that can pass for a build that never
happened. So it does not survive. There is deliberately no flag or environment
variable to disable this. `⇒ Up to date, nothing to do` is unaffected — that
fast exit means the last run succeeded and its outputs are intact.

## Mechanics (`src/cmd/staleoutputs.go`)

A binary at `build/<target>` is otherwise indistinguishable from one the current run produced. So an invocation that discards stdout+stderr and
ignores the exit code can execute a previous run's binary and report a build that never happened. The artifacts of the module's build targets are
therefore deleted:

1. `clearBuildOutputs` before any phase runs — `runWithRunner` (root, per module) and the top of `runReleaseWithRunner` (matrix/release), so a
   failure anywhere, a crash, or a kill leaves nothing runnable;
2. `discardBuildOutputs` on the failure path — deferred on the named error return of `run()` (registered FIRST so it runs LAST, after every phase
   has printed).
3. `discardBuildOutputsFromCWD` on the two exits that never enter the pipeline — the agent output guard's abort (which also NAMES the deleted paths
   in its message, so the missing binary doesn't read as a different bug) and, via the exported `DiscardBuildOutputs`, main's bootstrap-failure exit.

What counts as an artifact is `isOutputArtifact`: the bare name (`<name>.exe` and the cosmo fat APE), any `<name>_…` (BinaryName's
`<name>_<goos>_<goarch>[.exe]`, the wasm shapes, the `<name>_host` symlink), any `<name>.…` (the APE's sidecar ELFs), and the `.tmp-`-prefixed
spelling of all.

Discovery is a directory scan keyed on target NAME rather than a re-derivation of the platform matrix. So artifacts of a previous run's platform set
go too. `clearBuildOutputs` records `{dir, names}` per module (`trackedOutputs`, absolute) so the failure path works from any cwd in a multi-module
run. Removal failure is FATAL on the clear path (an undeletable binary is exactly the stale binary this prevents) and best-effort on the
failure/abort paths (never mask the real error). The "Up to date, nothing to do" fast exit is unaffected — it fires in `PersistentPreRunE` before
`run()`, and it means the last run succeeded with its outputs intact. No flag or env var disables any of this.

NOTE for dats suites: dats runs commands in the module root. So a suite test that execs a pipeline command must `cd "$(mktemp -d)"` first or it
deletes the binaries the pipeline just built (this bit `dats/cli.dats`'s guard test — see `dats/README.md`).

