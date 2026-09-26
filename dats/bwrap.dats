# Tests for .github/scripts/check-bwrap.sh, the gate action.yml runs after
# cached-apt installs bubblewrap. The script's contract: every Linux run gets
# either a usable bwrap or an error a caller can act on.

sandbox:
	image: golang:1.25

tests:
	# A module's own tree says nothing about what its dependencies generate, and
	# the go command confines each of those directives.
	- desc: a module with no dats directory still gets a backend
	  cmd: |
		set -u
		script="$PWD/.github/scripts/check-bwrap.sh"
		cd "$(mktemp -d)"
		out="$(bash "$script" 2>&1)"
		status=$?
		echo "$out"
		case "$status:$out" in
			0:*"bubblewrap is usable"*) echo "OUTCOME usable $(uname -s)" ;;
			1:*"::error::"*) echo "OUTCOME refused $(uname -s)" ;;
			*) echo "the script said something no caller can act on (exit $status)" >&2; exit 1 ;;
		esac
		case "$out" in
			*"nothing to provision"*) echo "the script skipped a run whose dependencies may generate" >&2; exit 1 ;;
		esac
	  outputs:
		stdout:
			- "OUTCOME "

	- desc: a module with dats suites gets an answer a caller can act on
	  cmd: |
		set -u
		out="$(bash .github/scripts/check-bwrap.sh 2>&1)"
		status=$?
		echo "$out"
		case "$status:$out" in
			0:*"bubblewrap is usable"*) echo "OUTCOME usable $(uname -s)" ;;
			1:*"::error::"*) echo "OUTCOME refused $(uname -s)" ;;
			*) echo "the script said something no caller can act on (exit $status)" >&2; exit 1 ;;
		esac
	  outputs:
		stdout:
			- "OUTCOME "

	# The script installs nothing, so a host with no bwrap fails and names the package.
	- desc: a host with no bwrap fails and names the install
	  cmd: 'PATH=/nonexistent "$(command -v bash)" .github/scripts/check-bwrap.sh'
	  exit: 1
	  outputs:
		stdout:
			- "::error::bubblewrap is not installed"
			- "apt-get install bubblewrap"
