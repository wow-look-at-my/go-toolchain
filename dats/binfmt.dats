# Tests for .github/scripts/register-ape-binfmt.sh, the step action.yml runs to
# let the kernel start a fat APE. The script's contract is that it NAMES what it
# did and never fails the job: a host that cannot register the handler is a host
# where every APE still starts through a shell, which is what every caller in
# this org already does.
#
# Nothing here registers anything. The sandbox holds no writable
# /proc/sys/fs/binfmt_misc and grants no root, so the script takes its
# stand-down path there and says so.
#
# build-everywhere runs this repo's pipeline on linux, darwin and windows, so
# these tests name no host: each pairs its answer with `uname -s`.

sandbox:
	image: golang:1.25

tests:
	- desc: the script names its outcome and exits clean
	  cmd: |
		set -eu
		out="$(bash .github/scripts/register-ape-binfmt.sh 2>&1)"
		echo "$out"
		case "$out" in
			"APE binfmt handler is registered" | "APE binfmt handler is already registered")
				echo "OUTCOME registered $(uname -s)" ;;
			"::warning::APE binfmt handler not registered: "*)
				echo "OUTCOME stood-down $(uname -s)" ;;
			*)
				echo "the script said something no caller can act on" >&2; exit 1 ;;
		esac
	  outputs:
		stdout:
			- "OUTCOME "

	- desc: a second run reaches the same outcome
	  cmd: |
		set -eu
		first="$(bash .github/scripts/register-ape-binfmt.sh 2>&1)"
		second="$(bash .github/scripts/register-ape-binfmt.sh 2>&1)"
		if [ "$first" != "$second" ]; then
			echo "first: $first" >&2
			echo "second: $second" >&2
			exit 1
		fi
		echo "STABLE $(uname -s)"
	  outputs:
		stdout:
			- "STABLE "

	- desc: a registered entry carries the APE header bytes and starts them with a shell
	  cmd: |
		set -eu
		entry=/proc/sys/fs/binfmt_misc/APE
		if [ ! -e "$entry" ]; then
			echo "ENTRY absent $(uname -s)"
			exit 0
		fi
		grep -qx 'magic 4d5a714670443d27' "$entry"
		grep -qx 'interpreter /bin/sh' "$entry"
		echo "ENTRY ok $(uname -s)"
	  outputs:
		stdout:
			- "ENTRY "
