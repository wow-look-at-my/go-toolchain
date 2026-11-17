# Tests for .github/scripts/build-from-github.sh, which the action runs to
# build the generators. A stub stands in for the go command and records its
# arguments, so no test here needs a network or a compiler.

tests:
	- desc: each build passes -trimpath, so the temporary checkout directory stays out of the build cache keys
	  cmd: |
		set -eu
		t=$(mktemp -d)
		git init --quiet "$t/repo"
		touch "$t/repo/go.mod"
		git -C "$t/repo" add go.mod
		git -C "$t/repo" -c user.name=test -c user.email=test@example.com commit --quiet -m init
		printf '%s\n' '#!/usr/bin/env bash' 'case "$*" in *"mod edit -json"*) echo "{}" ;; *) echo "$*" >> "$STUB_LOG" ;; esac' > "$t/go"
		chmod +x "$t/go"
		export STUB_LOG="$t/log" RUNNER_TEMP="$t"
		bash .github/scripts/build-from-github.sh "$t/go" "file://$t/repo" "" "$t/out" cmd/one cmd/two
		sed -e "s#$t#TMP#g" -e 's#build-from-github\.[A-Za-z0-9]*#CHECKOUT#' "$t/log"
	  outputs:
		stdout:
			- "-C TMP/CHECKOUT build -trimpath -o TMP/out/one ./cmd/one"
			- "-C TMP/CHECKOUT build -trimpath -o TMP/out/two ./cmd/two"
