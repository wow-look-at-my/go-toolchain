# The release stamp

A published binary reports the buildhost release number it shipped as. The number is an incrementing integer, so releases order by it. A commit hash does not order, so it rides along after the number.

## How the number gets in

The build itself stamps it. Nothing outside go-toolchain takes part, so no workflow can leave it out.

- A main package opts in by declaring `var releaseNumber string`, the way `var gitHash string` opts into the revision stamp ([VCS-STAMP.md](VCS-STAMP.md)).
- When the build links a package that declares it, `resolveRelease` (`src/cmd/releasenumber.go`) answers the number once per process. `releaseStamp` writes `-X <package>.releaseNumber=<number>` beside the revision stamp.
- In CI the number comes from buildhost. The build creates the project's release over the job's OIDC token, at the store the run locks use. The `buildhost-release` run lock holds the number, so every job of a run try stamps the same one. A job that finds it locked creates no release.
- The build writes `release_version` to `GITHUB_OUTPUT`. The action's publish names that release, so the uploaded binaries land in the release whose number they carry.
- `GO_TOOLCHAIN_RELEASE_VERSION` names the number for a build that already has one.

A failure to get the number fails the build. It never falls back to an unstamped binary.

A build outside CI with no `GO_TOOLCHAIN_RELEASE_VERSION` has no buildhost release. It warns, and the variable keeps its placeholder. Such a binary is never published.

## Printing it

A program prints `<name> <releaseNumber> (<gitHash>)`. An empty `releaseNumber` means the binary was not built for a release. The program says so as an error, not as a stand-in version.
