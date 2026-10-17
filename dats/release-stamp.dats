# Tests for .github/scripts/release-stamp.ts, which stamps each binary with its
# buildhost release before the action's publish uploads it. A wrong project or
# number here ships a binary whose stamp names a release that does not hold it.
# The repair step of action.yml loads that library through the typescript
# action's CommonJS .ts handler, so the test also asserts it uses no import.meta.

sandbox:
	image: node:24

tests:
	- desc: the stamp names the release of the project the publish uploads to, and the binary still runs
	  cmd: |
		node --version
		node .github/scripts/release-stamp.test.ts 2>&1
	  timeout: 5m
	  outputs:
		stdout:
			- "release-stamp: all checks passed"
