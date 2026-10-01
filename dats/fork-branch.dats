# Tests for .github/scripts/checkout-fork-branch.sh, which puts the fork
# submodule on the branch named like this checkout's branch.
#
# CI clones submodules shallow, and a shallow clone fetches one branch. The
# script must still reach any other branch, or host-build compiles the old
# gitlink while every later job compiles the branch head.

sandbox:
	image: golang:1.25

tests:
	- desc: a shallow submodule clone still moves to a branch it never fetched
	  cmd: |
		set -eu
		script="$PWD/.github/scripts/checkout-fork-branch.sh"
		root="$(mktemp -d)"
		export HOME="$root/home" GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
		mkdir -p "$HOME"
		git config --global protocol.file.allow always
		git config --global init.defaultBranch master
		git config --global url."file://$root/fork".insteadOf https://github.com/wow-look-at-my/fork.git
		git init -q "$root/fork"
		git -C "$root/fork" commit -q --allow-empty -m master
		git -C "$root/fork" checkout -q -b feature
		git -C "$root/fork" commit -q --allow-empty -m feature
		want="$(git -C "$root/fork" rev-parse HEAD)"
		git -C "$root/fork" checkout -q master
		git init -q "$root/super"
		cd "$root/super"
		git submodule add -q -b master https://github.com/wow-look-at-my/fork.git fork
		git commit -q -m super
		git clone -q --no-local "file://$root/super" "$root/clone"
		cd "$root/clone"
		git submodule update -q --init --depth 1
		mkdir -p .github/scripts
		cp "$script" .github/scripts/
		bash .github/scripts/checkout-fork-branch.sh feature
		test "$(git -C fork rev-parse HEAD)" = "$want" && echo "on the feature head"
	  outputs:
		stdout:
			- "on the feature head"
		stderr:
			- "fork: fork at feature"

	- desc: a branch the fork cannot supply fails the step, never builds the gitlink
	  cmd: |
		set -eu
		script="$PWD/.github/scripts/checkout-fork-branch.sh"
		root="$(mktemp -d)"
		export HOME="$root/home" GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
		mkdir -p "$HOME"
		git config --global protocol.file.allow always
		git config --global init.defaultBranch master
		git config --global url."file://$root/fork".insteadOf https://github.com/wow-look-at-my/fork.git
		git init -q "$root/fork"
		git -C "$root/fork" commit -q --allow-empty -m master
		git init -q "$root/super"
		cd "$root/super"
		git submodule add -q https://github.com/wow-look-at-my/fork.git fork
		git config -f .gitmodules submodule.fork.branch gone
		git commit -q -am super
		git checkout -q --detach
		mkdir -p .github/scripts
		cp "$script" .github/scripts/
		bash .github/scripts/checkout-fork-branch.sh "" && echo "NO FAILURE" || echo "failed"
	  outputs:
		stdout:
			- "failed"
		stderr:
			- "fork: cannot put fork on gone"
