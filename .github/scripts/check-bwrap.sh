#!/usr/bin/env bash
# Fails the job when bwrap cannot build a sandbox. The cached-apt step installs it.
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

if ! command -v bwrap > /dev/null 2>&1; then
	echo "::error::bubblewrap is not installed. A dependency's generate directive and the dats suites both need a sandbox backend. Install it with: apt-get install bubblewrap"
	exit 1
fi

if probe > /dev/null 2>&1; then
	echo "bubblewrap is usable"
	exit 0
fi

as_root sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 > /dev/null 2>&1 || true
# The package postinst sets this. cached-apt runs no postinst.
as_root sysctl -w kernel.unprivileged_userns_clone=1 > /dev/null 2>&1 || true

if ! probe; then
	echo "::error::bubblewrap is installed but cannot build a sandbox on this host. A dependency's generate directive and the dats suites both need one; see the bwrap error above."
	exit 1
fi
echo "bubblewrap is usable"
