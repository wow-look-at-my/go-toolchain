#!/usr/bin/env bash
# Refuse a key that appears twice in one step of a workflow or an action.
#
# GitHub rejects the whole file for it, before it creates any job. That shows
# up as a run with no jobs, no logs, and the workflow named by its path rather
# than its name, which is a long way from the edit that caused it.
#
# Nothing else here catches it. yq reads the file without complaint and keeps
# the last key, and the org's workflow lint reads comments rather than shape.
#
# Usage: check-step-keys.sh FILE...
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
