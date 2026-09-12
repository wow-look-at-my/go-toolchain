# Tests for .github/scripts/provision-bwrap.sh, the step action.yml runs so
# the dats phase has its sandbox backend. The script's contract: a module
# with no dats/ directory costs nothing, and a module that has suites gets
# either a usable bwrap or an error a caller can act on. Nothing here installs
# anything; the sandbox grants no root and no apt.

sandbox:
	image: golang:1.25

tests:
	- desc: a module with no dats directory is left alone
	  cmd: |
		set -eu
		dir="$(mktemp -d)"
		bash .github/scripts/provision-bwrap.sh "$dir"
		echo "EXIT $?"
	  outputs:
		stdout:
			- "nothing to provision"
			- "EXIT 0"

	- desc: a module with dats suites gets an answer a caller can act on
	  cmd: |
		set -u
		dir="$(mktemp -d)"
		mkdir "$dir/dats"
		out="$(bash .github/scripts/provision-bwrap.sh "$dir" 2>&1)"
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
