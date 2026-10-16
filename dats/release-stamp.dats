# Runs the tests of .github/scripts/release-stamp.ts. One of them loads the
# library the way action.yml does, through a CommonJS .ts handler. This is
# where ESM-only syntax in that file breaks every consumer.

sandbox:
	image: node:24

tests:
	- desc: release-stamp.test.ts passes under node --test
	  cmd: |
		set -eu
		node --version
		node --test --test-reporter=tap .github/scripts/release-stamp.test.ts
	  timeout: 5m
	  outputs:
		stdout:
			- "release-stamp: all checks passed"
			- "# fail 0"
