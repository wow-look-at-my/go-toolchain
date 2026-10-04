# Tests for .github/scripts/publishable-artifacts.sh, which decides whether the
# action's buildhost publish runs at all.
#
# Every executable binary publishes, and no input turns that off. So a build
# that produces none must skip the publish: buildhost-publish fails on a
# directory it can upload nothing from. A wrong answer here either loses a
# release or reds a library module's build.
#
# A job sets GITHUB_OUTPUT, and these tests inherit it. The file it names sits
# outside the sandbox, so each test that is not about the step output clears
# the variable and reads the stdout line instead.

sandbox:
	image: golang:1.25

tests:
	- desc: the portable manifest alone publishes
	  cmd: |
		set -eu
		dir="$(mktemp -d)/build"
		mkdir -p "$dir"
		echo '{"schema":1}' > "$dir/buildhost-artifacts.json"
		env -u GITHUB_OUTPUT bash .github/scripts/publishable-artifacts.sh "$dir"
	  outputs:
		stdout:
			- "goes to buildhost"

	- desc: a <binary>_{os}_{arch} name publishes, .exe and all
	  cmd: |
		set -eu
		dir="$(mktemp -d)/build"
		mkdir -p "$dir"
		touch "$dir/mytool_windows_amd64.exe"
		echo sums > "$dir/checksums.txt"
		env -u GITHUB_OUTPUT bash .github/scripts/publishable-artifacts.sh "$dir"
	  outputs:
		stdout:
			- "goes to buildhost"

	- desc: a library module builds nothing and publishes nothing
	  cmd: |
		set -eu
		dir="$(mktemp -d)/build"
		mkdir -p "$dir"
		env -u GITHUB_OUTPUT bash .github/scripts/publishable-artifacts.sh "$dir"
	  outputs:
		stdout:
			- "nothing to publish"

	- desc: a missing build directory publishes nothing
	  cmd: env -u GITHUB_OUTPUT bash .github/scripts/publishable-artifacts.sh "$(mktemp -d)/absent"
	  outputs:
		stdout:
			- "nothing to publish"

	- desc: the opt-out wasm name is outside the upload set, so it publishes nothing
	  cmd: |
		set -eu
		dir="$(mktemp -d)/build"
		mkdir -p "$dir"
		touch "$dir/mytool_js_wasm.wasm"
		echo sums > "$dir/checksums.txt"
		env -u GITHUB_OUTPUT bash .github/scripts/publishable-artifacts.sh "$dir"
	  outputs:
		stdout:
			- "nothing to publish"

	- desc: the answer reaches the step output GitHub reads
	  cmd: |
		set -eu
		dir="$(mktemp -d)/build"
		mkdir -p "$dir"
		touch "$dir/mytool_linux_amd64"
		GITHUB_OUTPUT="$dir/out.txt" bash .github/scripts/publishable-artifacts.sh "$dir"
		cat "$dir/out.txt"
	  outputs:
		stdout:
			- "publish=true"

	- desc: a step output the script cannot write fails the step
	  cmd: |
		set -eu
		dir="$(mktemp -d)/build"
		mkdir -p "$dir"
		touch "$dir/mytool_linux_amd64"
		GITHUB_OUTPUT="$dir/absent/out.txt" bash .github/scripts/publishable-artifacts.sh "$dir"
	  exit: 1
