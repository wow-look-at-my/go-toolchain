#!/usr/bin/env bash
# Put _gosmopolitan on the fork branch named like this one, or on the fork's
# default branch. This is what syncForkSource does mid-build (src/cmd/forksource.go),
# done here because make.bash and the generator install read the checkout first.
# The gitlink only decides where a fresh clone starts.
set -euo pipefail

remote=https://github.com/wow-look-at-my/gosmopolitan
branch="${GITHUB_REF_NAME:-}"

want=""
if [ -n "$branch" ]; then
	want="$(git ls-remote "$remote" "refs/heads/$branch" | awk '{print $1}')"
	if [ -n "$want" ]; then
		echo "gosmopolitan: following the branch named like this checkout, $branch, at $want"
	fi
fi
if [ -z "$want" ]; then
	want="$(git ls-remote "$remote" HEAD | awk '{print $1}')"
	echo "gosmopolitan: no branch named $branch there; following the default branch at $want"
fi
if [ -z "$want" ]; then
	echo "::error::$remote answered no commit for refs/heads/$branch or HEAD"
	exit 1
fi

git -C _gosmopolitan fetch --quiet --depth 1 origin "$want"
git -C _gosmopolitan checkout --quiet --detach "$want"
git -C _gosmopolitan submodule update --init --recursive
git -C _gosmopolitan rev-parse HEAD
