# The release stamp

A published binary carries the buildhost release it shipped as. The stamp is data that buildhost's publish appends to each binary before upload. It is not part of the build.

The action's auto-release step runs `buildhost-publish`. That step creates the release of each target project. It then appends a trailer to a copy of every binary, naming the project, the release number and the commit, and uploads the stamped copy. The number in a stamp is therefore always the number of the release that holds the file, in that project.

go-toolchain links every binary without a stamp, and creates no release. A binary built outside a publish has no stamp.

The format and a reader: `docs/release-stamp.md` in wow-look-at-my/buildhost.
