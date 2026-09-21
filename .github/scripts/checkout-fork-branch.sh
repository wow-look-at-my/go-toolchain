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

	# `.` means "the branch named like this one", which git resolves but
	# cannot fall back from. master is that fallback.
	branch=$(git config -f .gitmodules --get "submodule.$name.branch" || true)
	[[ "$branch" == "." || -z "$branch" ]] && branch=master
	if [[ -n "$here" ]] && git ls-remote --exit-code --heads "$url" "refs/heads/$here" >/dev/null 2>&1; then
		branch=$here
	fi

	git config "submodule.$name.branch" "$branch"
	if git submodule update --init --remote -- "$path"; then
		# The branch head names its own submodules, and cmd/dist reads
		# src/cmd/vendor as plain source. Each one is checked out at the
		# commit this head records, which is what make.bash compiles.
		git -C "$path" submodule update --init --recursive
		echo "fork: $path at $branch $(git -C "$path" rev-parse --short=12 HEAD)" >&2
	else
		echo "fork: $path stays where it is: cannot reach $url" >&2
	fi
done < <(git config -f .gitmodules --get-regexp '^submodule\..*\.path$' || true)
