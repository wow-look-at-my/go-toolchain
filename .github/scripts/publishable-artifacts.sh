#!/usr/bin/env bash

# buildhost-publish fails on a directory that holds nothing it can upload, so
# the action asks this first. The rule is buildhost-publish's own: the portable
# manifest, or a `<binary>_{os}_{arch}` name after .exe comes off.
set -euo pipefail

dir="${1:?usage: publishable-artifacts.sh <build dir>}"
manifest=buildhost-artifacts.json

publish=false
if [ -f "$dir/$manifest" ]; then
	publish=true
elif [ -d "$dir" ]; then
	for path in "$dir"/*; do
		[ -L "$path" ] && continue
		[ -f "$path" ] || continue
		name="${path##*/}"
		case "$name" in
			checksums.txt | "$manifest" | *.zip) continue ;;
		esac
		if [[ "${name%.exe}" =~ ^.+_[a-z]+_[a-z0-9]+$ ]]; then
			publish=true
			break
		fi
	done
fi

if [ "$publish" = true ]; then
	echo "publishable: $dir goes to buildhost"
else
	echo "publishable: no executable binary in $dir, nothing to publish"
fi
# The step output is how the answer reaches the job. A write that fails takes
# the step with it, because a publish decision nobody records is a decision
# nobody acts on. Outside a job there is no file, and the answer is the stdout
# line above.
if [ -n "${GITHUB_OUTPUT:-}" ]; then
	echo "publish=$publish" >> "$GITHUB_OUTPUT"
fi
