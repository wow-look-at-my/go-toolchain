# Tests for both scripts that install the go-toolchain binary.
#
# A stub curl on PATH plays buildhost, so no test needs the network.

sandbox:
	image: golang:1.25

tests:
	- desc: a file already at the path is a cache hit and runs no curl
	  cmd: |
		set -eu
		d="$(mktemp -d)"
		mkdir -p "$d/stub" "$d/bin"
		printf '#!/bin/sh\necho "curl ran" >&2\nexit 99\n' > "$d/stub/curl"
		chmod +x "$d/stub/curl"
		echo cached > "$d/bin/go-toolchain"
		PATH="$d/stub:$PATH" bash .github/scripts/fetch-go-toolchain.sh https://example.invalid/x "$d/bin/go-toolchain"
		cat "$d/bin/go-toolchain"
	  outputs:
		stdout:
			- "restored from the cache"
			- "cached"

	- desc: a stall is retried and the next attempt lands the binary
	  cmd: |
		set -eu
		d="$(mktemp -d)"
		mkdir -p "$d/stub"
		printf '%s\n' '#!/bin/sh' 'out=""' \
			'while [ $# -gt 0 ]; do [ "$1" = "-o" ] && { out="$2"; shift; }; shift; done' \
			'n=$(( $(cat "$COUNT" 2>/dev/null || echo 0) + 1 ))' \
			'echo "$n" > "$COUNT"' \
			'if [ "$n" -lt 3 ]; then echo partial > "$out"; exit 28; fi' \
			'echo binary > "$out"' > "$d/stub/curl"
		chmod +x "$d/stub/curl"
		COUNT="$d/count" GO_TOOLCHAIN_RETRY_SLEEP=0 PATH="$d/stub:$PATH" bash .github/scripts/fetch-go-toolchain.sh https://example.invalid/x "$d/bin/go-toolchain"
		cat "$d/bin/go-toolchain"
		test ! -e "$d/bin/go-toolchain.part"
	  outputs:
		stdout:
			- "attempt 1 stalled or dropped (curl exit 28)"
			- "attempt 2 stalled or dropped (curl exit 28)"
			- "downloaded on attempt 3"
			- "binary"

	- desc: an HTTP error ends the loop at once and leaves no file
	  cmd: |
		set -eu
		d="$(mktemp -d)"
		mkdir -p "$d/stub"
		printf '#!/bin/sh\necho x >> "$COUNT"\nexit 22\n' > "$d/stub/curl"
		chmod +x "$d/stub/curl"
		COUNT="$d/count" GO_TOOLCHAIN_RETRY_SLEEP=0 PATH="$d/stub:$PATH" bash .github/scripts/fetch-go-toolchain.sh https://example.invalid/x "$d/bin/go-toolchain"
		wc -l < "$d/count"
		test ! -e "$d/bin/go-toolchain"
		test ! -e "$d/bin/go-toolchain.part"
	  outputs:
		stdout:
			- "failed (curl exit 22)"
			- "1"

	- desc: the release number comes from the redirect
	  cmd: |
		set -eu
		d="$(mktemp -d)"
		mkdir -p "$d/stub"
		printf '#!/bin/sh\nprintf %%s "https://static.pazer.build/file?arch=amd64&fmt=raw&os=linux&project=go-toolchain&v=936"\n' > "$d/stub/curl"
		chmod +x "$d/stub/curl"
		PATH="$d/stub:$PATH" bash .github/scripts/resolve-go-toolchain-release.sh https://example.invalid/x
	  outputs:
		stdout:
			- "936"

	- desc: a redirect that names no release prints nothing and warns
	  cmd: |
		set -eu
		d="$(mktemp -d)"
		mkdir -p "$d/stub"
		printf '#!/bin/sh\nprintf %%s "https://static.pazer.build/file?project=go-toolchain"\n' > "$d/stub/curl"
		chmod +x "$d/stub/curl"
		out=$(PATH="$d/stub:$PATH" bash .github/scripts/resolve-go-toolchain-release.sh https://example.invalid/x 2>&1 >/dev/null)
		echo "$out"
		test -z "$(PATH="$d/stub:$PATH" bash .github/scripts/resolve-go-toolchain-release.sh https://example.invalid/x 2>/dev/null)"
	  outputs:
		stdout:
			- "names no release"

	- desc: a failed resolve prints nothing, so the cache step is skipped
	  cmd: |
		set -eu
		d="$(mktemp -d)"
		mkdir -p "$d/stub"
		printf '#!/bin/sh\nexit 6\n' > "$d/stub/curl"
		chmod +x "$d/stub/curl"
		test -z "$(PATH="$d/stub:$PATH" bash .github/scripts/resolve-go-toolchain-release.sh https://example.invalid/x 2>/dev/null)"
		echo resolved-nothing
	  outputs:
		stdout:
			- "resolved-nothing"
