#!/usr/bin/env bash
# Temporary. Runs the git step of action.yml verbatim, then ls-remotes private repos from a fresh directory.
set -u
: "${PRIVATE_ORG_REPO_READ:?no vended token}"

line="$(grep -m1 -E 'run: git config --global url\..*insteadOf' action.yml | sed -E 's/^[[:space:]]*run:[[:space:]]*//')"
echo "git: $(git --version)"
echo "step line from action.yml: ${line}"
eval "${line}"

for r in tml ansi-optimizer; do
	echo
	echo "=== ${r}"
	d="$(mktemp -d)"
	(
		cd "$d" || exit 1
		git init -q --bare .
		git remote add origin "https://github.com/wow-look-at-my/${r}"
		GIT_TERMINAL_PROMPT=0 GIT_TRACE_CURL=1 git ls-remote -q origin HEAD 2>&1 \
			| grep -E 'Send header: (GET|POST) |Send header: Authorization|Recv header: HTTP|warning:|fatal:|^[0-9a-f]{40}' \
			| sed -E 's/^[0-9:.]+ +http\.c:[0-9]+ +//' | cut -c1-150
		echo "exit status of ls-remote: ${PIPESTATUS[0]}"
	)
done
