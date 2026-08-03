# dats suites

Command-line tests for the freshly built go-toolchain binary, written in
[dats](https://github.com/wow-look-at-my/dats) YAML and executed by the
pipeline's dats phase after every build (root pipeline and `matrix`).

## Conventions (these apply to any repo built by go-toolchain)

- Suites are non-hidden `*.dats` files under `dats/` at the module root.
  When the directory has none, the phase is a silent no-op.
- A repo with **no `go.mod`** still gets its suites run: go-toolchain reports
  `No go.mod; running dats suites only`, and the suites are the entire run.
  Nothing was built, so `$GO_TOOLCHAIN_DATS_BUILD_DIR` is an empty directory
  and such a suite exercises what is already in the tree. This is how a shell
  or TypeScript repo uses dats without fetching a standalone binary and
  hand-wiring a CI step, at a version free to drift from the one linked here.
- go-toolchain **links the dats library** and runs suites in-process: there is
  no dats binary to install and no version to keep in step. The dats a build
  runs is the one go-toolchain was compiled against.
- The phase always runs **every** discovered test — there is no filtering,
  selection, or skip mechanism, by design (dats itself has none either).
- `$GO_TOOLCHAIN_DATS_BUILD_DIR` is exported to dats: a throwaway directory
  holding copies of the binaries the pipeline just built, named by their bare
  output name (`go-toolchain` here; plus `.exe` on windows hosts). Tests exec
  binaries through it — never out of `build/` directly, because matrix cosmo
  slot artifacts are APEs that self-assimilate on first exec.
- That directory is `build/.dats-stage/` **inside the module root**, and it is
  there for a reason: dats sandboxes every command, and of the host a
  sandboxed command reaches only the working directory (docker mounts it and
  nothing else; bwrap binds the OS tool tree plus the cwd, with a private
  `/tmp` over it). A staging dir under `$TMPDIR` is invisible to both, and
  every suite fails its setup command. `build/` is gitignored in every repo
  go-toolchain builds, so staging there never dirties the tree.
- Suites are **sandboxed** — the phase does not pass `--no-sandbox`, and it is
  not the toolchain's call to make. The handoff dir is READ-ONLY there, like
  the rest of the working directory, and there is no way to declare otherwise:
  a test whose binary must write (an APE rewrites its own file on first exec)
  copies it into the private `/tmp` first and execs the copy — see
  `cli.dats`. A suite whose commands genuinely need the host says so per file
  (`sandbox: false`); one that needs something specific of the docker backend
  names it (`image:`).
- Suites run serially (`dats test dats`, no `-j`) so the report is
  byte-deterministic and staged APE copies never race their first exec.
- dats runs each command in the **module root**, and go-toolchain deletes the
  module's build outputs on any run that does not succeed. A test that execs
  a pipeline command must therefore `cd` into a throwaway directory first, or
  it deletes the binaries the pipeline just built (`d="$(mktemp -d)"; cd "$d";
  …`). Tests that only exec `$GO_TOOLCHAIN_DATS_BUILD_DIR` copies with `--help`
  or `version` are unaffected — neither reaches the pipeline.
- Snapshot goldens live in `<suite>.snapshots/` next to the suite (e.g.
  `dats/cli.snapshots/` for `dats/cli.dats`) and are committed. Regenerate
  after intentional CLI changes with `dats --update test dats` and review the
  diff. A stale golden is a red run. The golden's filename carries the test's
  INDEX, so inserting a test above a snapshot test renames its golden — add
  new tests at the end of the suite, or regenerate.

## Notes specific to this repo's suite

- Every test sets `GO_TOOLCHAIN_BUILDHOST_URL` to an unreachable address so
  the background update check fails instantly and silently, keeping output
  deterministic. Consumer suites that exec go-toolchain itself should do the
  same.
- The agent-output-guard tests in `cli.dats` assume a **linux host**, because
  this suite only runs when this repo builds ITSELF, which only happens on
  linux (`build`/`host-build`). darwin has its own real guard classifier
  (`src/cmd/claudeguard_darwin.go`) and its own dats coverage, but that suite
  cannot live under this repo's `dats/` — every suite here runs during this
  repo's own linux self-build too, and a suite that execs a native darwin
  binary would fail there. Instead `.github/workflows/ci.yml`'s smoke-macos
  job writes one inline (as a heredoc, like the throwaway module's own
  go.mod/main.go) into a throwaway module and runs it against the actual
  published darwin/arm64 binary — inline rather than a checked-in template,
  because that job never runs `actions/checkout` (only a release-artifact
  download), so there is no repo tree there to copy a template out of.
- CI provisions **bubblewrap** before running the pipeline (`.github/workflows/ci.yml`,
  `host-build` and `build`), so suites run under the native Linux sandbox rather than
  the docker fallback, and an unusable bwrap fails the job instead of silently
  degrading to it.
- The suite pins `sandbox: image: golang:1.25`. Every go-toolchain invocation
  past `version` bootstraps a Go toolchain, so under the docker backend (what
  CI falls back to when bwrap is unavailable) an image without Go would make
  each command download one. bwrap and seatbelt ignore `image` and use the
  host's Go.
- `version`'s staleness footer varies with GitHub reachability, so tests
  assert only the stable `Version:`/`Commit:` lines.
