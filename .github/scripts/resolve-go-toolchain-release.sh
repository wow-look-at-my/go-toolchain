#!/usr/bin/env bash
# Prints the release number that buildhost's redirect for URL names.
set -euo pipefail
url="$1"
if ! location=$(curl -fsS -o /dev/null -w '%{redirect_url}' --connect-timeout 20 --max-time 60 "${url}"); then
	echo "::warning::Could not resolve the go-toolchain release from ${url}. The binary downloads without the cache." >&2
	exit 0
fi
release=$(printf '%s' "${location}" | sed -nE 's/.*[?&]v=([0-9]+).*/\1/p')
if [ -z "${release}" ]; then
	echo "::warning::The go-toolchain redirect names no release: ${location}. The binary downloads without the cache." >&2
	exit 0
fi
echo "${release}"
