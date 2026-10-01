# dats suites

Command-line tests for the freshly built go-toolchain binary, written in [dats](https://github.com/wow-look-at-my/dats) YAML and executed by the pipeline's dats phase after every build (root pipeline and `matrix`).

What a repository built by go-toolchain can rely on -- the suite layout, the tab-only dialect, `$GO_TOOLCHAIN_DATS_BUILD_DIR` and its staging directory, the sandbox contract, serial execution and the goldens -- is dats' own documentation: `dats docs embedding`, or `docs/embedding.md` in that repository.

## Notes specific to this repo's suite

- Every test sets `GO_TOOLCHAIN_BUILDHOST_URL` to an unreachable address so the background update check fails instantly and silently, keeping output deterministic. Consumer suites that exec go-toolchain itself must do the same. A test running `version` (not `version raw`) sets `GO_TOOLCHAIN_GITHUB_API_URL` the same way: the staleness footer is a separate query against api.github.com.
- No test in `cli.dats` names a host. `build-everywhere` runs this repo's whole pipeline on linux, darwin and NT. So the suite runs on all three. A test whose answer differs by host prints that answer next to `uname -s`, and the pattern accepts only the pairs that agree. The SHIPPED artifact is a separate question, answered by `.github/dats-fixtures/smoke.dats` -- one file for every host.
- CI provisions **bubblewrap** before running the pipeline (`.github/workflows/ci.yml`, `host-build` and `build`). So suites run under the native Linux sandbox rather than the docker fallback.
- The suite pins `sandbox: image: golang:1.25`. Every go-toolchain invocation past `version` bootstraps a Go toolchain. So under the docker backend (what CI falls back to when bwrap is unavailable) an image without Go will make each command download one. bwrap and seatbelt ignore `image` and use the host's.
- `version`'s staleness footer varies with GitHub reachability, so tests assert only the stable `Version:`/`Commit:` lines.
- The `$GO_TOOLCHAIN_DATS_BUILD_DIR` copy of this repo's binary is named `go-toolchain`, plus `.exe` on windows hosts.
