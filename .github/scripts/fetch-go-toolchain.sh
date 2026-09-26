#!/usr/bin/env bash
# Puts the go-toolchain binary at BIN. A file already at BIN is a cache hit.
set -euo pipefail
url="$1"
bin="$2"
retry_sleep="${GO_TOOLCHAIN_RETRY_SLEEP:-5}"
mkdir -p "$(dirname "${bin}")"
if [ -s "${bin}" ]; then
	echo "go-toolchain restored from the cache: ${bin}"
	exit 0
fi
attempt=0
while :; do
	attempt=$((attempt + 1))
	rc=0
	curl -fL --compressed --connect-timeout 20 --speed-limit 262144 --speed-time 30 -o "${bin}.part" "${url}" || rc=$?
	if [ "${rc}" -eq 0 ]; then
		mv "${bin}.part" "${bin}"
		echo "go-toolchain downloaded on attempt ${attempt}: ${bin}"
		exit 0
	fi
	rm -f "${bin}.part"
	case "${rc}" in
	6 | 7 | 18 | 28 | 35 | 52 | 55 | 56 | 92)
		echo "::warning::go-toolchain download attempt ${attempt} stalled or dropped (curl exit ${rc}). Retrying in ${retry_sleep} s."
		sleep "${retry_sleep}"
		;;
	*)
		echo "::warning::Download from buildhost failed (curl exit ${rc}): ${url}"
		exit 0
		;;
	esac
done
