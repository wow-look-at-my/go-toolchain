# One binary for every platform (`matrix`)
See [WASM.md](WASM.md) for the `wasm/js` and `wasm/wasip1` targets, which share
this command and its fork toolchain.

> **Shipping policy.** The `wow-look-at-my` org ships **one APE covering every
> supported platform**. The fat APE is the only native output, and there is no
> flag for a per-platform native binary: `--targets` accepts only `cosmo` and
> the wasm targets below.
>
> This is not a property of `matrix` alone. The gosmopolitan fork is the
> pipeline's **only** compiler — `EnsureGoVersion` puts it on `PATH` and
> `GOROOT` with `GOTOOLCHAIN=local` before any phase runs, and there is no
> go.dev download path left — so a bare `go-toolchain` builds the same APE that
> `matrix` publishes. `runBuild` is the single place anything is compiled, and
> `checkPortableJob` refuses there any job naming a native platform or carrying
> no fork GOROOT. Every variable that picks the compiler or the target
> (`GOOS`, `GOARCH`, `GOROOT`, `PATH`, `CGO_ENABLED`) is assigned rather than
> inherited, so an ambient `GOOS=linux` cannot ask for a native binary either.

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
saves nothing. An APE carries one payload per ARCHITECTURE, and those three
platforms still need both — darwin/arm64 boots the arm64 image, linux/amd64 and
windows/amd64 the amd64 one. Measured saving for the default set: **0%**. Only
collapsing to a single architecture drops a payload (**-46.9%**). The win of the
default is one artifact instead of six, not fewer bytes.

Accepted: `linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`.
`darwin/amd64` (Intel-mac runtime never proven on real hardware) and
`windows/arm64` (amd64-only PE payload) are refused — a published platform set
says where the binary runs, so an unproven host cannot be in it.

**Publishing.** The APE publishes as a *single* artifact carrying its whole
platform set. One upload, one download link, one checksum, with an
`APE:<platforms>` badge. go-toolchain writes `buildhost-artifacts.json`
alongside the binary to say so — see
[BUILDHOST-MANIFEST.md](BUILDHOST-MANIFEST.md).

**Adding wasm targets.** `--targets` takes `cosmo` and/or the wasm targets
(`wasm/js`, `wasm/wasip1`) — nothing else. Leaving `cosmo` out of the list
builds wasm alone:

```bash
go-toolchain matrix --targets cosmo,wasm/js
go-toolchain matrix --targets wasm/js,wasm/wasip1
```

**No per-platform copies.** A cosmo build writes the APE and nothing else.
There is no flag that copies it onto `<name>_<os>_<arch>` names — the APE
publishes under its own name through the manifest.

**Toolchain resolution.** Building the cosmo target needs the gosmopolitan
toolchain:

1. `GO_TOOLCHAIN_COSMO_GOROOT` — path to a local gosmopolitan build's GOROOT;
   used directly, nothing is downloaded.
2. Otherwise it is downloaded from buildhost
   (`https://dl.pazer.build/gosmopolitan?branch=<GO_TOOLCHAIN_COSMO_BRANCH>`,
   default branch `master`) and cached under
   `~/.cache/go-toolchain/cosmo/v<N>/` keyed by the buildhost release version,
   so it downloads once per release. Every host asks for its own `os`/`arch`.
   Buildhost decides what exists, and a host it publishes nothing for fails
   with that answer plus the `GO_TOOLCHAIN_COSMO_GOROOT` escape. Nothing here
   keeps a list of supported hosts — one went stale and refused darwin/arm64
   while buildhost was serving it.

**Build semantics.** The cosmo build always runs with `CGO_ENABLED=0`
(cosmopolitan has no cgo; `--cgo` warns and is ignored for this target) and
without `GOARCH` (fat, covering amd64+arm64, is the fork's default output).

**Reproducible across build hosts.** Every build passes `-trimpath` and
`-ldflags=-buildid=`, so the same source compiles to the same bytes wherever it
is built. Two inputs vary between runners and each flag closes one. `-trimpath`
drops the paths: where the source was checked out, and where the toolchain was
installed. `-ldflags=-buildid=` empties the linked binary's Go build ID, which
is the only channel the toolchain's own identity reaches the output.

Measured on the fork, same source. Two checkout paths differ by 200 bytes
without `-trimpath`, and a differing tool ID differs by about 160 bytes with
`-trimpath` alone. Both are the Go build-ID note and the GNU build-ID note, one
pair per payload — never code. With both flags the builds are byte-identical.

The cost is that `go tool buildid` on a shipped artifact returns empty. Only
the final link is affected: a cached package archive keeps the stamp the cache
poison guards read, so [CACHE.md](CACHE.md) is untouched. Action IDs still
differ per host, so this buys identical bytes and never a cross-host cache hit.

`-buildid=` is the tail of a longer `-ldflags` value. The revision stamp and
whatever the caller put in `GOFLAGS` come ahead of it, and
[VCS-STAMP.md](VCS-STAMP.md) covers why the order is what it is. Neither part
varies by host — the stamp is the commit, which every runner in a CI run shares
— so the `identical` job still holds.

**Cache isolation.** Fork-toolchain builds (cosmo and wasm) run with their
cache keys namespaced by a content hash of the toolchain in use
(`GO_TOOLCHAIN_CACHE_NAMESPACE`, set automatically). The fork stamps a constant
version, so different fork builds would otherwise collide on cache keys and
serve each other stale objects (SIGSEGV binaries). Namespaced builds skip the
shared cache daemon and cache per-toolchain. Every build is a fork build, so
every build is namespaced. See
[CACHE.md](CACHE.md#fork-toolchain-key-namespacing).

**An APE keeps its bytes when it runs.** The kernel cannot exec the file as it
stands, so the bootstrap stages a copy under `$TMPDIR` and writes the host's
native header into THAT. The artifact keeps its checksum, which is what makes
comparing one host's APE against another's meaningful at all. Measured: running
a built APE twice leaves its sha256 unchanged. Depth: gosmopolitan's
`docs/APE-STAGING.md`.

That staging needs a SHELL to read the header, and `execve` alone cannot. A
direct exec works only where binfmt_misc carries an `APE` entry, which
registering needs root; macOS has no such mechanism. `action.yml` registers that
entry on a Linux runner and warns where it cannot (see
[ACTION.md](ACTION.md#1b3-the-ape-binfmt-handler)), so the entry is a capability
a host. Every caller still reaches an APE through a shell,
and nothing may assume a bare `exec` of one succeeds.

