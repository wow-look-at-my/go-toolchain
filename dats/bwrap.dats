<<<<<<< HEAD
# Tests for .github/scripts/provision-bwrap.sh, the step action.yml runs so
# tidy and the dats phase have their sandbox backend. The script's contract:
# every module gets either a usable bwrap or an error a caller can act on,
# whether or not it has suites. Nothing here installs anything; the sandbox
# grants no root and no apt.
=======
# Tests for .github/scripts/provision-bwrap.sh, the step action.yml runs so the
# build has its sandbox backend. The script's contract: every Linux run gets
# either a usable bwrap or an error a caller can act on. Nothing here installs
# anything; the sandbox grants no root and no apt.
>>>>>>> origin/master

sandbox:
	image: golang:1.25

tests:
<<<<<<< HEAD
	# The run happens in a directory with no dats/ suites. Tidy needs the
	# backend there. A script that skips that case fails this test.
	- desc: a module with no dats directory still gets an answer a caller can act on
	  cmd: |
		set -u
		root="$PWD"
		dir="$(mktemp -d)"
		out="$(cd "$dir" && bash "$root/.github/scripts/provision-bwrap.sh" 2>&1)"
=======
	# A module's own tree says nothing about what its dependencies generate, and
	# the go command confines each of those directives.
	- desc: a module with no dats directory still gets a backend
	  cmd: |
		set -u
		script="$PWD/.github/scripts/provision-bwrap.sh"
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
		out="$(bash .github/scripts/provision-bwrap.sh 2>&1)"
>>>>>>> origin/master
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
