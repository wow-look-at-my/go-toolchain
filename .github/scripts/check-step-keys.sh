#!/usr/bin/env bash

# GitHub rejects a whole file for a repeated step key, before it makes any job.
set -euo pipefail

status=0
for file in "$@"; do
	found=$(awk '
		# A sequence item opens a step. Its keys sit one level in from the dash.
		/^[ ]*-[ ]/ {
			match($0, /^ */)
			keyindent = RLENGTH + 2
			split("", seen)
			item = FNR
			next
		}
		# Only keys at that exact indent are the step own. Deeper ones belong to
		# with:, env: and the rest, where the same name is free to appear again.
		{
			match($0, /^ */)
			if (keyindent == 0 || RLENGTH != keyindent) next
			if (!match($0, /^ *[A-Za-z_][A-Za-z0-9_.-]*:/)) next
			key = substr($0, RSTART, RLENGTH)
			gsub(/[ :]/, "", key)
			if (key in seen) {
				printf "%s:%d: the step at line %d already has a %s key\n", FILENAME, FNR, item, key
			}
			seen[key] = 1
		}
	' "$file")
	if [ -n "$found" ]; then
		printf '%s\n' "$found"
		status=1
	fi
done

if [ "$status" -eq 0 ]; then
	echo "OK -- every step names each key once in $# file(s)"
fi
exit "$status"
