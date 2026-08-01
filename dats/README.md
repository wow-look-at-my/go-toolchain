# dats suites

Command-line tests for the freshly built go-toolchain binary, written in
[dats](https://github.com/wow-look-at-my/dats) YAML and executed by the
pipeline's dats phase after every build (root pipeline and `matrix`).

## Conventions (these apply to any repo built by go-toolchain)

- Suites are non-hidden `*.dats` files under `dats/` at the module root.
  When the directory has none, the phase is a silent no-op.
- The phase always runs **every** discovered test — there is no filtering,
  selection, or skip mechanism, by design (dats itself has none either).
- `$GO_TOOLCHAIN_DATS_BUILD_DIR` is exported to dats: a throwaway directory
  holding copies of the binaries the pipeline just built, named by their bare
  output name (`go-toolchain` here; plus `.exe` on windows hosts). Tests exec
  binaries through it — never out of `build/` directly, because matrix cosmo
  slot artifacts are APEs that self-assimilate on first exec.
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
- The agent-output-guard tests assume a **linux host**: the guard classifier
  is compiled for linux||cosmo and is a documented no-op on native
  darwin/windows — the same scoping as the smoke-linux guard gate in CI
  (which is the only CI leg that runs this repo's pipeline on the repo).
- `version`'s staleness footer varies with GitHub reachability, so tests
  assert only the stable `Version:`/`Commit:` lines.
