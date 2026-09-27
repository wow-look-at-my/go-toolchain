#!/usr/bin/env bash
# Put each org submodule on its branch head, as gosmopolitan's own
# src/submodulebranch.bash does: name the branch for git, then let
# `git submodule update --remote` read it. make.bash and the generator
# install read the checkout, so this runs before either.
# CI passes the branch in, because a checkout there is detached.
set -euo pipefail

cd "$(dirname "$0")/../.."
[[ -f .gitmodules ]] || exit 0

here=${1:-}
if [[ -z "$here" ]]; then
	here=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo HEAD)
	[[ "$here" == HEAD ]] && here=""
fi

while read -r key _; do
	name=${key#submodule.}
	name=${name%.path}

	path=$(git config -f .gitmodules --get "submodule.$name.path")
	url=$(git config -f .gitmodules --get "submodule.$name.url")
	case "$url" in
	*/github.com/wow-look-at-my/*) ;;
	*) continue ;;
	esac

	# CI resolves the fork once per run, so every job builds the same commit.
	if [[ "$path" == _gosmopolitan && -n "${GO_TOOLCHAIN_FORK_COMMIT:-}" ]]; then
		commit=$GO_TOOLCHAIN_FORK_COMMIT
		if ! git submodule update --init -- "$path" ||
			! git -C "$path" fetch --quiet --depth=1 origin "$commit" ||
			! git -C "$path" checkout --quiet --detach "$commit" ||
			! git -C "$path" submodule update --init --recursive; then
			echo "fork: cannot put $path at $commit, the commit this run resolved" >&2
			exit 1
		fi
		echo "fork: $path at $commit, the commit this run resolved" >&2
		continue
	fi

	# `.` means "the branch named like this", which git resolves but cannot fall back from. master is that fallback.
	branch=$(git config -f .gitmodules --get "submodule.$name.branch" || true)
	[[ "$branch" == "." || -z "$branch" ]] && branch=master
	if [[ -n "$here" ]] && git ls-remote --exit-code --heads "$url" "refs/heads/$here" >/dev/null 2>&1; then
		branch=$here
	fi

	git config "submodule.$name.branch" "$branch"
	# A shallow submodule clone fetches one branch, and --remote reads only the tracking ref.
	# The head names its own submodules, and make.bash compiles the commits it records.
	if ! git submodule update --init -- "$path" ||
		! git -C "$path" fetch --depth=1 origin "+refs/heads/$branch:refs/remotes/origin/$branch" ||
		! git submodule update --remote -- "$path" ||
		! git -C "$path" submodule update --init --recursive; then
		echo "fork: cannot put $path on $branch from $url" >&2
		exit 1
	fi
	echo "fork: $path at $branch $(git -C "$path" rev-parse --short=12 HEAD)" >&2
done < <(git config -f .gitmodules --get-regexp '^submodule\..*\.path$' || true)
