# Tests for .github/scripts/probe-go-toolchain.sh, the check action.yml runs on
# a downloaded release before it uses it. The binary is also the go command it
# links as "go", so the probe runs it under both names. A release whose
# "version" works and whose go command cannot start must fail the probe, or the
# action never falls back to a source build.

sandbox:
	image: golang:1.25

tests:
	- desc: a binary that runs under both names passes
	  inputs:
		files:
			fake.sh: |
				#!/bin/sh
				echo "ran as $(basename "$0") $*"
	  cmd: |
		set -eu
		chmod +x {inputs.fake.sh}
		bash .github/scripts/probe-go-toolchain.sh {inputs.fake.sh} {outputs.probe}
	  exit: 0
	  outputs:
		stdout:
			- "ran as fake.sh version"
			- "ran as go"

	- desc: a binary whose go command cannot start fails with the go command's own output
	  inputs:
		files:
			fake.sh: |
				#!/bin/sh
				case "$(basename "$0")" in
					go | go.exe)
						echo "go: cannot find GOROOT directory"
						exit 2 ;;
				esac
				echo "ran as $(basename "$0") $*"
	  cmd: |
		chmod +x {inputs.fake.sh}
		bash .github/scripts/probe-go-toolchain.sh {inputs.fake.sh} {outputs.probe}
	  exit: 1
	  outputs:
		stdout:
			- "ran as fake.sh version"
			- "go: cannot find GOROOT directory"

	- desc: a go command that answers version but cannot find its GOROOT fails
	  inputs:
		files:
			fake.sh: |
				#!/bin/sh
				if [ "$1" = env ]; then
					echo "go: cannot find GOROOT directory"
					exit 2
				fi
				echo "ran as $(basename "$0") $*"
	  cmd: |
		chmod +x {inputs.fake.sh}
		bash .github/scripts/probe-go-toolchain.sh {inputs.fake.sh} {outputs.probe}
	  exit: 1
	  outputs:
		stdout:
			- "ran as go version"
			- "go: cannot find GOROOT directory"

	- desc: a binary that cannot run at all fails before the go command is tried
	  inputs:
		files:
			fake.sh: |
				#!/bin/sh
				echo "exec format error"
				exit 126
	  cmd: |
		chmod +x {inputs.fake.sh}
		bash .github/scripts/probe-go-toolchain.sh {inputs.fake.sh} {outputs.probe}
	  exit: 1
	  outputs:
		stdout:
			- "exec format error"
