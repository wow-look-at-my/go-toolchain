#!/usr/bin/env bash
# Put each org submodule on its branch head, as gosmopolitan's own
# src/submodulebranch.bash does: name the branch for git, then let
# `git submodule update --remote` read it. make.bash and the generator
# install read the checkout, so this runs before either.
# CI passes the branch in, because a checkout there is detached.
set -euo pipefail

cd "$(dirname "$0")/../.."
[[ -f .gitmodules ]] || exit 0

# --resolve prints "path=sha" per org submodule and moves nothing. CI runs it once, so every job builds the same fork commit.
resolve=""
if [[ "${1:-}" == --resolve ]]; then
	resolve=1
	shift
fi
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

	# `.` means "the branch named like this", which git resolves but
	# cannot fall back from. master is that fallback.
	branch=$(git config -f .gitmodules --get "submodule.$name.branch" || true)
	[[ "$branch" == "." || -z "$branch" ]] && branch=master
	if [[ -n "$here" ]] && git ls-remote --exit-code --heads "$url" "refs/heads/$here" >/dev/null 2>&1; then
		branch=$here
	fi

	if [[ -n "$resolve" ]]; then
		sha=$(git ls-remote "$url" "refs/heads/$branch" | awk '{print $1}')
		[[ -n "$sha" ]] || { echo "fork: cannot resolve $url $branch" >&2; exit 1; }
		echo "$path=$sha"
		continue
	fi
	pinned=""
	for pin in ${FORK_HEADS:-}; do
		[[ "${pin%%=*}" == "$path" ]] && pinned=${pin#*=}
	done
	if [[ -n "$pinned" ]]; then
		git submodule update --init -- "$path"
		git -C "$path" fetch -q origin "$pinned"
		git -C "$path" checkout -q --detach "$pinned"
		echo "fork: $path at pinned $pinned" >&2
		continue
	fi
	git config "submodule.$name.branch" "$branch"
	if git submodule update --init --remote -- "$path"; then
		echo "fork: $path at $branch $(git -C "$path" rev-parse --short=12 HEAD)" >&2
	else
		echo "fork: $path stays where it is: cannot reach $url" >&2
	fi
done < <(git config -f .gitmodules --get-regexp '^submodule\..*\.path$' || true)
