#!/usr/bin/env bash
# Provisions bubblewrap on a Linux runner whose module has dats suites. The
# dats phase sandboxes every suite command, and without bwrap it falls back to
# docker, which runs them in a container with no host Go for the bootstrap.
#
# A module with no dats/ directory pays nothing. A host where bwrap already
# works pays a probe. A host where it cannot work fails here, with its own
# error, instead of degrading to the fallback unnoticed.
#
# usage: provision-bwrap.sh <working-directory>
set -euo pipefail

readonly workdir="${1:-.}"

if [ ! -d "$workdir/dats" ]; then
	echo "no dats suites under $workdir; nothing to provision"
	exit 0
fi

as_root() {
	if [ "$(id -u)" -eq 0 ]; then
		"$@"
	elif command -v sudo > /dev/null 2>&1; then
		sudo "$@"
	else
		return 1
	fi
}

# probe builds the smallest sandbox dats builds. A bwrap that is installed
# but refused by the kernel fails here, not inside the suites.
probe() {
	bwrap --ro-bind-try /usr /usr --ro-bind-try /bin /bin --ro-bind-try /lib /lib \
		--ro-bind-try /lib64 /lib64 --ro-bind-try /etc /etc \
		--dev /dev --proc /proc --tmpfs /tmp --chdir / --unshare-pid --die-with-parent true
}

if command -v bwrap > /dev/null 2>&1 && probe > /dev/null 2>&1; then
	echo "bubblewrap is usable"
	exit 0
fi

if ! command -v bwrap > /dev/null 2>&1; then
	if ! command -v apt-get > /dev/null 2>&1; then
		echo "::error::bubblewrap is not installed and there is no apt-get to install it. The dats suites need a sandbox backend; install bwrap on this host."
		exit 1
	fi
	if ! as_root apt-get update > /dev/null || ! as_root apt-get install -y bubblewrap > /dev/null; then
		echo "::error::could not install bubblewrap. The dats suites need a sandbox backend."
		exit 1
	fi
fi

# Ubuntu 24.04 refuses an unprivileged user namespace unless this is off.
as_root sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 > /dev/null 2>&1 || true

if ! probe; then
	echo "::error::bubblewrap is installed but cannot build a sandbox on this host. The dats suites need one; see the bwrap error above."
	exit 1
fi
echo "bubblewrap is usable"
