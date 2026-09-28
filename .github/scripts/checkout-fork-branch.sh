#!/usr/bin/env bash
# Put each org submodule on its branch head, as gosmopolitan's own
# src/submodulebranch.bash does: name the branch for git, then let
# `git submodule update --remote` read it. make.bash and the generator
# install read the checkout, so this runs before either.
# CI passes the branch in, because a checkout there is detached.
set -euo pipefail

cd "$(dirname "$0")/../.."
[[ -f .gitmodules ]] || exit 0

# run_lock NAME HEAD prints what buildhost's run lock holds for NAME in this attempt, and claims HEAD when it holds nothing.
run_lock() {
	local store=${GOSMOPOLITAN_RUN_LOCK_STORE:-https://pazer.build} token answer
	token=$(curl -fsS -H "Authorization: Bearer $ACTIONS_ID_TOKEN_REQUEST_TOKEN" \
		"$ACTIONS_ID_TOKEN_REQUEST_URL&audience=$(jq -rn --arg s "$store" '$s|@uri')" | jq -er .value)
	answer=$(curl -fsS -H "Authorization: Bearer $token" --get "$store/api/v1/run-locks" \
		--data-urlencode "repository=$GITHUB_REPOSITORY" --data-urlencode "run_id=$GITHUB_RUN_ID" \
		--data-urlencode "run_attempt=$GITHUB_RUN_ATTEMPT" --data-urlencode "name=$1")
	if [[ $(jq -r '.found // false' <<<"$answer") != true ]]; then
		[[ -n "$2" ]] || { echo "fork: $1 has no head to lock" >&2; return 1; }
		answer=$(jq -n --arg r "$GITHUB_REPOSITORY" --arg i "$GITHUB_RUN_ID" --arg a "$GITHUB_RUN_ATTEMPT" --arg n "$1" --arg v "$2" \
			'{repository: $r, run_id: $i, run_attempt: $a, name: $n, value: $v}' |
			curl -fsS -H "Authorization: Bearer $token" -H 'Content-Type: application/json' --data-binary @- "$store/api/v1/run-locks")
	fi
	jq -er '.value | select(. != "")' <<<"$answer"
}

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

	# `.` means "the branch named like this", which git resolves but cannot fall back from. master is that fallback.
	branch=$(git config -f .gitmodules --get "submodule.$name.branch" || true)
	[[ "$branch" == "." || -z "$branch" ]] && branch=master
	if [[ -n "$here" ]] && git ls-remote --exit-code --heads "$url" "refs/heads/$here" >/dev/null 2>&1; then
		branch=$here
	fi

	# In CI the fork takes the head this run attempt locked in buildhost, as the go command does for an org module.
	if [[ "$path" == _gosmopolitan && "${GITHUB_ACTIONS:-}" == true ]]; then
		head=$(git ls-remote --heads "$url" "refs/heads/$branch" | cut -f1)
		commit=$(run_lock "github.com/wow-look-at-my/gosmopolitan@$branch" "$head")
		if ! git submodule update --init -- "$path" ||
			! git -C "$path" fetch --quiet --depth=1 origin "$commit" ||
			! git -C "$path" checkout --quiet --detach "$commit" ||
			! git -C "$path" submodule update --init --recursive; then
			echo "fork: cannot put $path at $commit, the head this run locked on $branch" >&2
			exit 1
		fi
		echo "fork: $path at $branch $commit, locked for this run" >&2
		continue
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
