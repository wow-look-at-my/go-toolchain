# Runs the tests of .github/scripts/release-stamp.ts. The repair step of
# action.yml loads that library through the typescript action's CommonJS .ts
# handler. The consumer CI job runs that step for real.

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
