# One binary for every platform (`matrix`)
See [WASM.md](WASM.md) for the `wasm/js` and `wasm/wasip1` targets, which share
this command and its fork toolchain.

> **Shipping policy.** The `wow-look-at-my` org ships **one APE covering every
> supported platform**. Per-platform binaries have ended: the org no longer
> maintains or stores them, and nothing in its pipelines produces them. The
> cross-compilation flags below still work for consumers with a genuine need,
> but they are not this org's shipping mode.

`go-toolchain matrix` builds **one** file: a fat Actually Portable Executable,
compiled with the [gosmopolitan](https://github.com/wow-look-at-my/gosmopolitan)
Go fork (`GOOS=cosmo`). It runs natively on Linux x64, macOS ARM64 and Windows
x64 — no emulation, no per-platform download. The artifact is
`<name>` (no `.exe`, though the file is a genuine PE polyglot).

```bash
go-toolchain matrix
# build/go-toolchain        the binary, runs on all three
# build/buildhost-artifacts.json      publishes it as ONE artifact
# build/checksums.txt
```

**Choosing the platforms.** `--cosmo-platforms` takes `os/arch` pairs and
defaults to `linux/amd64,darwin/arm64,windows/amd64`. `all` covers everything
the fork can emit.

A narrower set is **not** automatically a smaller binary, and the default set
saves nothing: an APE carries one payload per ARCHITECTURE, and those three
platforms still need both — darwin/arm64 boots the arm64 image, linux/amd64 and
windows/amd64 the amd64 one. Measured saving for the default set: **0%**. Only
collapsing to a single architecture drops a payload (**-46.9%**). The win of the
default is one artifact instead of six, not fewer bytes.

Accepted: `linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`.
`darwin/amd64` (Intel-mac runtime never proven on real hardware) and
`windows/arm64` (amd64-only PE payload) are refused — a published platform set
says where the binary runs, so an unproven host cannot be in it.

**Publishing.** The APE publishes as a *single* artifact carrying its whole
platform set: one upload, one download link, one checksum, with an
`APE:<platforms>` badge. go-toolchain writes `buildhost-artifacts.json`
alongside the binary to say so — see
[BUILDHOST-MANIFEST.md](BUILDHOST-MANIFEST.md).

**Per-platform binaries instead** — supported, but not this org's shipping
mode. Naming `--os` or `--arch` builds the cartesian product of native
binaries; naming only one fills the other in (`--arch arm64` means every OS,
arm64). `--targets` takes an exact list of `os/arch` pairs plus the entry
`cosmo`, and mixes them freely:

```bash
go-toolchain matrix --os linux,darwin --arch amd64,arm64
go-toolchain matrix --targets cosmo,windows/arm64
```

**No per-platform copies.** A cosmo build writes the APE and nothing else.
There is no flag that copies it onto `<name>_<os>_<arch>` names — the APE
publishes under its own name through the manifest, as one artifact carrying its
whole platform set.

**Toolchain resolution.** Building the cosmo target needs the gosmopolitan
toolchain:

1. `GO_TOOLCHAIN_COSMO_GOROOT` — path to a local gosmopolitan build's GOROOT;
   used directly, nothing is downloaded.
2. Otherwise it is downloaded from buildhost
   (`https://dl.pazer.build/gosmopolitan?branch=<GO_TOOLCHAIN_COSMO_BRANCH>`,
   default branch `master`) and cached under
   `~/.cache/go-toolchain/cosmo/v<N>/` keyed by the buildhost release version,
   so it downloads once per release. Prebuilt toolchains exist for linux/amd64
   hosts only today; on other hosts set `GO_TOOLCHAIN_COSMO_GOROOT`.

**Build semantics.** The cosmo build always runs with `CGO_ENABLED=0`
(cosmopolitan has no cgo; `--cgo` warns and is ignored for this target) and
without `GOARCH` (fat, covering amd64+arm64, is the fork's default output).

**Cache isolation.** Fork-toolchain builds (cosmo and wasm) run with their
cache keys namespaced by a content hash of the toolchain in use
(`GO_TOOLCHAIN_CACHE_NAMESPACE`, set automatically). The fork stamps a constant
version, so different fork builds would otherwise collide on cache keys and
serve each other stale objects (SIGSEGV binaries). Namespaced builds skip the
shared cache daemon and cache per-toolchain; normal targets are unaffected.
See [CACHE.md](CACHE.md#fork-toolchain-key-namespacing).

**Heads-up: APEs self-assimilate.** Executing an APE rewrites its own header
in place to the host's native format, making the file differ from its
checksum. Never execute the artifacts in `build/` directly — that includes the
local `<name>`/`<name>_host` convenience symlinks, which point at the APE when
no native host binary was built. Run a throwaway copy instead. The build
pipeline itself never executes matrix artifacts in place (the dats phase stages
copies; benchmarks compile their own test binaries), so artifacts stay pristine
through the build.

