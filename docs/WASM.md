# WebAssembly targets

Sibling of [MATRIX.md](MATRIX.md), which covers the native targets of the same
`matrix` command.

`matrix --targets` also accepts the two WebAssembly platforms: `wasm/js`
(browser / Node.js, run with `wasm_exec.js`) and `wasm/wasip1` (WASI runtimes
such as wasmtime or wazero) — spelled os-first to match buildhost's wasm
artifact scheme and the `<name>_wasm_js` artifact naming. The GOOS-order
spellings `js/wasm` and `wasip1/wasm` are accepted as compatibility aliases
and normalize to the same targets (mixing both spellings dedupes to one
target). Wasm targets mix freely with native pairs and `cosmo` in one run:

```bash
go-toolchain matrix --targets wasm/js,wasm/wasip1,linux/amd64
```

The same pairing also works through the `--os`/`--arch` cartesian product
(and thus the action's `os:`/`arch:` inputs): `--os wasm` combines only with
the wasm flavor arches `js`/`wasip1`, producing the identical targets —
`--os wasm --arch js` is `--targets wasm/js` (same artifacts, naming, and
per-target main discovery). In a mixed list the impossible cross
combinations (`wasm` with a native arch, a native os with `js`/`wasip1`) are
skipped with one aggregate warning; if the whole product is impossible
(`--os wasm --arch amd64` alone) the build fails fast, and a `js`/`wasip1`
arch without `wasm` anywhere in `--os` is an error naming the fix. A
wasm-only consumer's action config is simply:

```yaml
with:
  os: wasm
  arch: js
```

**Per-target main-package discovery.** With an explicit `--targets` list,
main packages are discovered under **each target's own build context**
(GOOS/GOARCH), not the host's: a main package guarded `//go:build js && wasm`
(e.g. a browser entry point importing `syscall/js`) is built for `wasm/js`
targets and never attempted for native ones, an unconstrained main builds for
every target as before, and a `//go:build linux` main builds for `linux/*`
entries even from a non-linux host. A target whose context has no main
packages at all is skipped with a warning (a target list where **no** entry
has any main packages is still an error). The `cosmo` pseudo-target keeps
host-context discovery (the fat APE spans several native platforms), and the
legacy `--os` x `--arch` product keeps host-context discovery exactly as
before.

**Toolchain.** Wasm targets are built with the same
[gosmopolitan](https://github.com/wow-look-at-my/gosmopolitan) fork toolchain
as the cosmo target (resolution is identical: `GO_TOOLCHAIN_COSMO_GOROOT`,
else a buildhost download selected by `GO_TOOLCHAIN_COSMO_BRANCH`, cached
under `~/.cache/go-toolchain/cosmo/`) — the fork carries this org's wasm
runtime fixes (default-on preemptible loops, Node.js `fetch` networking,
synchronous stdout under node, CPU profiling, DWARF debug info; see the fork's
`WASM_SHORTCOMINGS.md`). The fork defaults to `GOOS=cosmo`, so wasm builds
always pin `GOOS`/`GOARCH` explicitly and run with `GOTOOLCHAIN=local` and
`CGO_ENABLED=0` (wasm has no cgo; `--cgo` warns and is ignored for these
targets).

**Artifacts.** Wasm binaries are named `<name>_wasm_js` /
`<name>_wasm_wasip1` — buildhost's wasm artifact convention (`os=wasm` with
`arch=js`/`arch=wasip1`), with the order deliberately swapped relative to
`GOOS_GOARCH` and **no file extension**: the publish pipeline parses
artifacts from the trailing two underscore-separated filename tokens after
stripping only `.exe`, so the bare form is what publishes as
`os=wasm`/`arch=js|wasip1` (an extension would keep the file out of the
upload set entirely). The files are still ordinary wasm modules, covered by
`checksums.txt`.

**Buildhost publishing.** By default wasm artifacts are published to
buildhost like any other target, as `os=wasm` with `arch=js`/`arch=wasip1`.
This **requires a buildhost with wasm artifact support**
([buildhost#166](https://github.com/wow-look-at-my/buildhost/pull/166)); on
an older server the upload is 400-rejected (`invalid os "wasm"` — the same
validation that rejects `os=cosmo`, and that rejected the pre-convention
`os=js` naming with `invalid os "js"` in the field) and a single rejected
artifact aborts the whole publish. The build logs a warning whenever wasm
targets are built, naming the requirement and the opt-out. For consumers
whose buildhost predates wasm support, set **`GO_TOOLCHAIN_WASM_PUBLISH=0`**:
wasm artifacts then take the excluded `<name>_<goos>_wasm.wasm` naming, whose
`.wasm` suffix keeps them outside the publish upload set (the same skip that
covers `checksums.txt` and `profile.json`) while the real files remain in
`build/` and `checksums.txt` for any downstream step to pick up. With the opt-out active, a **wasm-only** target list leaves the
publish step nothing to upload and it fails with "No matrix artifacts" —
disable `autorelease` in that combination (the build logs a warning for this
case too). Without the opt-out, wasm-only publishes are fine once the server
has wasm support.

**wasm_exec.js.** A `wasm/js` build also copies the fork toolchain's
`lib/wasm/wasm_exec.js` — the JS harness that loads the wasm in a browser or
Node, which must byte-match the toolchain that built it — into
`build/wasm_exec.js`. It is covered by `checksums.txt` and stays in `build/`,
but sits outside the buildhost publish set (its name doesn't match
the publish pipeline's `<binary>_{os}_{arch}` pattern, like `checksums.txt`
itself). Missing harness in the fork GOROOT only warns.

**GOMEMLIMIT guard.** The injected cgroup guard is stdlib-only and compiles
for both wasm ports; without cgroup files it is a startup no-op, so wasm
binaries are built from the same guarded source as every other target. The
guard is injected into main packages visible under the **host** context only;
a main that exists only under a cross-compile context (such as a
`js && wasm`-guarded browser entry point) gets no guard — sound, since the
guard reads Linux cgroup limits and would no-op there anyway. Discovery skips
the guard file by name, so an injected (or stale) guard never makes a
host-only main dir look like a main package for another target.

**Running and testing wasm binaries.** The build pipeline never executes
matrix artifacts, and the test phase always runs on the HOST platform — wasm
builds do not change what `go test` tests. To run the artifacts or execute a
package's tests under wasm, use the fork toolchain's exec wrappers in
`<goroot>/lib/wasm` (`go_js_wasm_exec` needs Node.js 18+; `go_wasip1_wasm_exec`
needs wasmtime, or wazero via `GOWASIRUNTIME=wazero`):

```bash
GOROOT=$HOME/.cache/go-toolchain/cosmo/<key>/go
PATH="$GOROOT/bin:$GOROOT/lib/wasm:$PATH" GOTOOLCHAIN=local \
  GOOS=js GOARCH=wasm go test ./...
```

Rejected spellings fail fast with a pointer to the right one: `js`/`wasip1`
in `--os` and `wasm` in `--arch` (both flipped in buildhost's model — use
`--os wasm --arch js|wasip1`, or `--targets wasm/js`/`wasm/wasip1`),
`js/amd64`, `linux/wasm` and `wasm/amd64` (impossible pairings), a
`js`/`wasip1` arch with no `wasm` os in the list, and a wasm target in
`--cosmo-platforms` (an APE covers native hosts, and wasm is not one).

