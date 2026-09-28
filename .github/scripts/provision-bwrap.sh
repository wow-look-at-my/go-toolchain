#!/usr/bin/env bash
# Provisions bubblewrap on a Linux runner. Phases need it.
set -euo pipefail

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
		echo "::error::bubblewrap is not installed and there is no apt-get to install it. A dependency's generate directive and the dats suites both need a sandbox backend; install bwrap on this host."
		exit 1
	fi
	if ! as_root apt-get update > /dev/null || ! as_root apt-get install -y bubblewrap > /dev/null; then
		echo "::error::could not install bubblewrap. A dependency's generate directive and the dats suites both need a sandbox backend."
		exit 1
	fi
fi

as_root sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 > /dev/null 2>&1 || true

if ! probe; then
	echo "::error::bubblewrap is installed but cannot build a sandbox on this host. A dependency's generate directive and the dats suites both need one; see the bwrap error above."
	exit 1
fi
echo "bubblewrap is usable"
