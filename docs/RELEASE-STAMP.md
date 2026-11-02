# The release stamp

A published binary reports the buildhost release number it shipped as. The number is an incrementing integer, so releases order by it. A commit hash does not order, so it rides along after the number.

## How the number gets in

The build itself stamps it, into every binary it links. No program declares anything, and nothing outside go-toolchain and the gosmopolitan go command takes part, so no workflow or program can leave it out.

- Before the first main package links, `ensureReleaseEnv` (`src/cmd/releasenumber.go`) puts `GOSMOPOLITAN_RELEASE` and `GOSMOPOLITAN_RELEASE_REVISION` in the process environment. Every go command go-toolchain starts inherits them.
- The fork's go command writes them into every binary's build info as the settings `release` and `release.revision` (`cmd/go/internal/load/release.go` in gosmopolitan). They are independent of `-buildvcs` and of the build running in a checkout.
- In CI the number comes from buildhost. The build creates the release over the job's OIDC token, at the store the run locks use. It creates it in the project the publish uploads the first linked binary to. This covers the repo's project, or `<repo>/<name>` for a binary named apart from the repo (`publishProject`, the same rule as buildhost-publish). A release in any other project counts its numbers apart, and the publish finds that number already published. The run lock `buildhost-release:<project>` holds the number, so every job of a run try that publishes to that project stamps the same one. Jobs that publish to other projects lock their own numbers. A job that finds it locked creates no release.
- The build writes `release_version` to `GITHUB_OUTPUT`. The action's publish names that release, so the uploaded binaries land in the release whose number they carry.
- `GO_TOOLCHAIN_RELEASE_VERSION` names the number for a build that already has one.

In CI, a failure to get the number or the commit fails the build. So does a failure to hand the number to the publish. It never falls back to an unstamped binary.

After each link, `verifyReleaseStamp` reads the binary's build info with the same go command and fails the build unless it carries this release and commit. A go command without gosmopolitan's release settings ignores the variables. This check is what turns that into a red build instead of an unstamped release.

A build outside CI with no `GO_TOOLCHAIN_RELEASE_VERSION` has no buildhost release. It warns, and its binaries carry no release setting. Such a binary is never published.

## Reading it

```go
bi, _ := debug.ReadBuildInfo()
for _, s := range bi.Settings {
	switch s.Key {
	case "release": // the buildhost release number
	case "release.revision": // the commit it was built from
	}
}
```

A program prints `<name> <release> (<release.revision>)`. A missing `release` means the binary was not built for a release. The program says so as an error, not as a stand-in version.

## The version variable

A program that keeps its version in a variable gets it filled, with no code that reads the build info. `releaseVarLDFlags` (`src/cmd/releasevar.go`) writes `-X` for each `Version` or `version` string variable in the main package. It does the same for each package of the module that the main package imports from a non-test file. That covers the cobra layout, where `main` calls `cmd.Execute` and the root command reads `cmd.Version`. The value is `<release> (<release.revision>)`, so cobra's own `--version` prints `<name> version 53 (f870077…)`.

A build with no release leaves the variable alone. The program keeps its placeholder.

The stamp goes on the link only. The linker flags are part of the link action ID (`linkActionID` in `cmd/go/internal/work/exec.go`) and of no compile action ID. A new release number changes no compile result in the build cache. The tests never get the flag.
