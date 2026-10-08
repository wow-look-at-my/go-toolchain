#!/bin/sh
# Runs this checkout's ratchet tests against the branch checked out at $1, and fails unless each passes there.
set -eu
head=$1
tests=src/cmd/ratchet_test.go
tree=$(mktemp -d)
trap 'git -C "$head" worktree remove --force "$tree"' EXIT
git -C "$head" worktree add --quiet --detach "$tree" HEAD
cp "$tests" "$tree/$tests"
names=$(grep -o '^func Test[A-Za-z0-9_]*' "$tests" | cut -c6- | paste -sd'|' -)
want=$(grep -c '^func Test' "$tests")
out=$(cd "$tree" && go test -count=1 -v -run "^($names)\$" ./src/cmd 2>&1) || {
	printf '%s\n' "$out"
	exit 1
}
printf '%s\n' "$out"
got=$(printf '%s\n' "$out" | grep -c -- '^--- PASS: ')
if [ "$got" -ne "$want" ]; then
	echo "ratchet: $got of the default branch's $want ratchet tests passed on this branch" >&2
	exit 1
fi
