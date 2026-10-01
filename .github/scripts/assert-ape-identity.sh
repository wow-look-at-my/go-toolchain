#!/usr/bin/env bash
# Assert every host built the same APE bytes. Lives in a script (not inline in
# ci.yml) so the no-tests-in-yaml guard does not read it as a test written into
# a workflow file.
set -uo pipefail
for o in linux darwin windows; do
  if [ ! -f "ape/$o/go-toolchain" ]; then
    echo "::error::$o handed off no APE, so identity across hosts is untested, not proven"
    exit 1
  fi
done
sha256sum ape/*/go-toolchain
rc=0
for o in darwin windows; do
  cmp -s ape/linux/go-toolchain "ape/$o/go-toolchain" || {
    echo "::error::the $o build differs from the linux build: the APE is one binary for every host, so every host has to produce it. Compare with cmp -l; the build-ID notes are what -trimpath and -ldflags=-buildid= exist to close (docs/MATRIX.md)"
    rc=1
  }
done
exit $rc
