# Tests for .github/scripts/release-stamp.ts, which stamps each binary with its
# buildhost release before the action's publish uploads it. A wrong project or
# number here ships a binary whose stamp names a release that does not hold it.

sandbox:
	image: node:22

tests:
	- desc: the stamp names the release of the project the publish uploads to, and the binary still runs
	  cmd: node .github/scripts/release-stamp.test.ts
	  outputs:
		stdout:
			- "release-stamp: all checks passed"
