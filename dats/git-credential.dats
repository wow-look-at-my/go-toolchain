# Runs the tests of .github/scripts/git-credential.test.ts. They check that no
# workflow puts the token in the username alone, and that the git step
# authenticates against a local Basic-auth server.

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
