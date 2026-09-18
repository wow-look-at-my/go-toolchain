#!/usr/bin/env bash
<<<<<<< HEAD
# Provisions bubblewrap on every Linux runner. Two phases need it. The dats
# phase sandboxes every suite command, and without bwrap it falls back to
# docker, which runs them in a container with no host Go for the bootstrap.
# `go mod tidy` also confines a dependency's generate directives in bwrap, and
# it refuses the directive outright when bwrap is missing. That path belongs to
# every module, so a module with no dats/ directory still needs the backend.
#
# A host where bwrap already works pays a probe. A host where it cannot work
# fails here, with its own error, instead of degrading to the fallback
# unnoticed.
=======
# Provisions bubblewrap on a Linux runner. Two phases need it. The dats phase
# sandboxes every suite command. The go command confines the generate directive
# of every dependency that carries one, and stops the build when it cannot.
#
# So every Linux build needs a backend, not only a module with dats suites: a
# module's own tree says nothing about what its dependencies generate. A host
# where bwrap already works pays a probe. A host where it cannot work fails
# here, with its own error, instead of degrading unnoticed.
>>>>>>> origin/master
#
# usage: provision-bwrap.sh
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
<<<<<<< HEAD
		echo "::error::bubblewrap is not installed and there is no apt-get to install it. Tidy and the dats suites need a sandbox backend; install bwrap on this host."
		exit 1
	fi
	if ! as_root apt-get update > /dev/null || ! as_root apt-get install -y bubblewrap > /dev/null; then
		echo "::error::could not install bubblewrap. Tidy and the dats suites need a sandbox backend."
=======
		echo "::error::bubblewrap is not installed and there is no apt-get to install it. A dependency's generate directive and the dats suites both need a sandbox backend; install bwrap on this host."
		exit 1
	fi
	if ! as_root apt-get update > /dev/null || ! as_root apt-get install -y bubblewrap > /dev/null; then
		echo "::error::could not install bubblewrap. A dependency's generate directive and the dats suites both need a sandbox backend."
>>>>>>> origin/master
		exit 1
	fi
fi

# Ubuntu 24.04 refuses an unprivileged user namespace unless this is off.
as_root sysctl -w kernel.apparmor_restrict_unprivileged_userns=0 > /dev/null 2>&1 || true

if ! probe; then
<<<<<<< HEAD
	echo "::error::bubblewrap is installed but cannot build a sandbox on this host. Tidy and the dats suites need a sandbox; see the bwrap error above."
=======
	echo "::error::bubblewrap is installed but cannot build a sandbox on this host. A dependency's generate directive and the dats suites both need one; see the bwrap error above."
>>>>>>> origin/master
	exit 1
fi
echo "bubblewrap is usable"
