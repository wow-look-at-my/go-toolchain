# Tests for .github/scripts/check-step-keys.sh, which refuses a key that
# appears again in a single step.
#
# GitHub rejects the whole file for that, before it creates any job. The run
# carries no jobs and no logs, and names the workflow by its path rather than
# its name, so nothing points back at the edit that caused it. This repo spent
# a CI round on exactly that.
#
# The check has to earn its place by failing: each case below builds a file and
# names the answer, rather than trusting this repo's own workflows to stay
# broken for the test's benefit.

sandbox:
	image: golang:1.25

tests:
	- desc: a step naming one key twice is refused, with the line of each
	  cmd: |
		set -eu
		check="$PWD/.github/scripts/check-step-keys.sh"
		dir="$(mktemp -d)"
		printf 'jobs:\n  a:\n    steps:\n      - name: two envs\n        env:\n          X: "1"\n        env:\n          Y: "2"\n        run: true\n' > "$dir/w.yml"
		bash "$check" "$dir/w.yml" && echo "REFUSAL MISSING" || echo "refused"
	  outputs:
		stdout:
			- "the step at line 4 already has a env key"
			- "refused"

	- desc: a name under with: is not the step's own name
	  cmd: |
		set -eu
		check="$PWD/.github/scripts/check-step-keys.sh"
		dir="$(mktemp -d)"
		printf 'jobs:\n  a:\n    steps:\n      - name: outer\n        uses: ./x\n        with:\n          name: inner\n          path: p\n' > "$dir/w.yml"
		bash "$check" "$dir/w.yml"
	  outputs:
		stdout:
			- "OK -- every step names each key once"

	- desc: this repo's own workflows and action pass
	  cmd: bash .github/scripts/check-step-keys.sh .github/workflows/ci.yml action.yml
	  outputs:
		stdout:
			- "OK -- every step names each key once"
