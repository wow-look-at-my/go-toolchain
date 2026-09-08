#!/usr/bin/env bash
# Registers the APE binfmt_misc handler, so the kernel hands a fat APE to
# /bin/sh and a bare execve of one succeeds. Without it only a shell can start
# an APE, and `go run`, `go test` and any exec from a program fail with
# "exec format error".
#
# A host that cannot register the handler keeps working: every caller in this
# org already reaches an APE through a shell. So this warns and exits 0.
set -euo pipefail

readonly dir=/proc/sys/fs/binfmt_misc
readonly entry="$dir/APE"
# The header bytes of a fat APE spell MZqFpD=' and /bin/sh reads the rest.
readonly line=':APE:M::\x4d\x5a\x71\x46\x70\x44\x3d\x27::/bin/sh:'

# stands_down names what is unavailable, because a caller that assumed the
# entry is there gets "exec format error" and no reason for it.
stands_down() {
	echo "::warning::APE binfmt handler not registered: $1. An APE still starts through a shell, and a bare exec of one still fails."
	exit 0
}

as_root() {
	if [ "$(id -u)" -eq 0 ]; then
		"$@"
	elif command -v sudo > /dev/null 2>&1; then
		sudo "$@"
	else
		return 1
	fi
}

if [ -e "$entry" ]; then
	read -r state < "$entry" || state=unknown
	if [ "$state" = enabled ]; then
		echo "APE binfmt handler is already registered"
		exit 0
	fi
	stands_down "an APE entry is already present and reads '$state'"
fi

if [ ! -e "$dir/register" ] && ! as_root mount -t binfmt_misc binfmt_misc "$dir" > /dev/null 2>&1; then
	stands_down "this host has no mounted $dir"
fi

if ! printf '%s\n' "$line" | as_root tee "$dir/register" > /dev/null 2>&1; then
	stands_down "the write to $dir/register was refused"
fi

if [ ! -e "$entry" ]; then
	stands_down "the write to $dir/register succeeded and left no entry"
fi

echo "APE binfmt handler is registered"
