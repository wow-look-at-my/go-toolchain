# dats suites

Command-line tests for the freshly built go-toolchain binary, written in
[dats](https://github.com/wow-look-at-my/dats) YAML and executed by the
pipeline's dats phase after every build (root pipeline and `matrix`).

## Conventions (these apply to any repo built by go-toolchain)

- Suites are non-hidden `*.dats` files under `dats/` at the module root.
  When the directory has none, the phase is a silent no-op.
- **Indent with tabs.** dats parses with `wow-look-at-my/yaml-fixed`, which
  inverts stock YAML. A space in the leading indentation is a parse error
  (`spaces cannot be used for indentation`), and spaces may only align after a
  tab. So a sequence item's sibling keys line up under the content after its
  `- ` marker — one tab of depth plus two spaces of alignment:

  ```
  tests:
  → - desc: something
  → ··cmd: echo hi
  → ··outputs:
  → → stdout:
  → → → - hi
  ```

  (`→` a tab, `·` a space.)
- A repo with **no `go.mod`** still gets its suites run: go-toolchain reports
  `No go.mod; running dats suites only`, and the suites are the entire run.
  Nothing was built, so `$GO_TOOLCHAIN_DATS_BUILD_DIR` is an empty directory
  and such a suite exercises what is already in the tree. This is how a shell
  or TypeScript repo uses dats without fetching a standalone binary.
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
  there for a reason. Dats sandboxes every command, and of the host a
  sandboxed command reaches only the working directory (docker mounts it and
  nothing else; bwrap binds the OS tool tree plus the cwd, with a private
  `/tmp` over it). A staging dir under `$TMPDIR` is invisible to both, and
  every suite fails its setup command. `build/` is gitignored in every repo
  go-toolchain builds, so staging there never dirties the tree.
- Suites are **sandboxed** — the phase does not pass `--no-sandbox`, and it is
  not the toolchain's call to make. The handoff dir is READ-ONLY there, like
  the rest of the working directory, and there is no way to declare otherwise.
  A test whose binary must write (an APE rewrites its own file on first exec)
  copies it into the private `/tmp` first and execs the copy — see
  `cli.dats`. A suite whose commands genuinely need the host says so per file
  (`sandbox: false`); one that needs something specific of the docker backend
  names it (`image:`).
- Suites run serially (`dats test dats`, no `-j`) so the report is
  byte-deterministic and staged APE copies never race their first exec.
- dats runs each command in the **module root**, and go-toolchain deletes the
  module's build outputs on any run that does not succeed. A test that execs.
- Snapshot goldens live in `<suite>.snapshots/` next to the suite (e.g.
  `dats/cli.snapshots/` for `dats/cli.dats`) and are committed. Regenerate
  after intentional CLI changes with `dats --update test dats` and review the
  diff. A stale golden is a red run. The golden's filename carries the test's.

## Notes specific to this repo's suite

- Every test sets `GO_TOOLCHAIN_BUILDHOST_URL` to an unreachable address so
  the background update check fails instantly and silently, keeping output
  deterministic. Consumer suites that exec go-toolchain itself should do the
  same. A test running `version` (not `version raw`) sets
  `GO_TOOLCHAIN_GITHUB_API_URL` the same way: the staleness footer is a
  separate query against api.github.com.
- The agent-output-guard tests in `cli.dats` name **no host**. `build-everywhere`
  runs this repo's whole pipeline on linux, darwin and NT, so the suite runs on
  all three. Each guard test prints its answer next to `uname -s` and the
  pattern accepts only the pairs that agree. That is what keeps one file
  covering a guard whose correct answer differs by host. The SHIPPED artifact is a separate question, and a committed
  fixture answers it:
  `.github/dats-fixtures/agent-output-guard.dats` — one file for every host.
- CI provisions **bubblewrap** before running the pipeline (`.github/workflows/ci.yml`,
  `host-build` and `build`). So suites run under the native Linux sandbox rather than
  the docker fallback.
- The suite pins `sandbox: image: golang:1.25`. Every go-toolchain invocation
  past `version` bootstraps a Go toolchain. So under the docker backend (what
  CI falls back to when bwrap is unavailable) an image without Go would make
  each command download one. bwrap and seatbelt ignore `image` and use the
  host's.
- `version`'s staleness footer varies with GitHub reachability, so tests
  assert only the stable `Version:`/`Commit:` lines.
