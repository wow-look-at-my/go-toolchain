# Runs the tests of .github/scripts/git-credential.test.ts. They check that no
# workflow rewrites a URL with a token, and that the credential helper step
# authenticates git against a local Basic-auth server.

sandbox:
	image: node:24

tests:
	- desc: git-credential.test.ts passes under node
	  cmd: |
		set -eu
		node --version
		git --version
		node .github/scripts/git-credential.test.ts
	  timeout: 5m
	  outputs:
		stdout:
			- "git-credential: all checks passed"
