#!/usr/bin/env bash
# Installs the cosmocc toolchain in /opt/cosmocc, pinned by digest.
set -euo pipefail

readonly version=4.0.2
readonly sha256=85b8c37a406d862e656ad4ec14be9f6ce474c1b436b9615e91a55208aced3f44
readonly dir=/opt/cosmocc

as_root() {
	if [ "$(id -u)" -eq 0 ]; then
		"$@"
	else
		sudo "$@"
	fi
}

# A caller that put its own cosmocc on PATH keeps it.
if command -v x86_64-unknown-cosmo-cc > /dev/null 2>&1 && command -v aarch64-unknown-cosmo-cc > /dev/null 2>&1; then
	echo "cosmocc is already on PATH at $(command -v x86_64-unknown-cosmo-cc)"
	exit 0
elif [ -x "$dir/bin/x86_64-unknown-cosmo-cc" ] && [ -x "$dir/bin/aarch64-unknown-cosmo-cc" ]; then
	echo "cosmocc is already installed in $dir"
else
	zip="${RUNNER_TEMP:-/tmp}/cosmocc-$version.zip"
	curl -fsSL "https://cosmo.zip/pub/cosmocc/cosmocc-$version.zip" -o "$zip"
	# macOS ships shasum and no sha256sum.
	if command -v sha256sum > /dev/null 2>&1; then
		echo "$sha256  $zip" | sha256sum -c -
	else
		echo "$sha256  $zip" | shasum -a 256 -c -
	fi
	as_root mkdir -p "$dir"
	as_root unzip -q -o "$zip" -d "$dir"
	rm -f "$zip"
fi

# Read it all, then print a line: a pipe into head kills the compiler with SIGPIPE, which pipefail reads as a failure.
said="$("$dir/bin/x86_64-unknown-cosmo-cc" --version 2>&1)"
printf '%s\n' "${said%%$'\n'*}"
if [ -n "${GITHUB_PATH:-}" ]; then
	echo "$dir/bin" >> "$GITHUB_PATH"
fi
