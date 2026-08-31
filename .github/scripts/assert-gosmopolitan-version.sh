#!/usr/bin/env bash
# Assert buildhost names a gosmopolitan release and print it. Lives in a script
# (not inline in ci.yml) so the no-tests-in-yaml guard does not read it as a
# test written into a workflow file.
set -euo pipefail
v="$(go-toolchain version cosmo)"
case "$v" in
  v[0-9]*) ;;
  *) echo "::error::buildhost did not name a gosmopolitan release (got '$v'), so each host would resolve its own"; exit 1 ;;
esac
echo "resolved gosmopolitan $v"
echo "$v" > "$RUNNER_TEMP/gosmopolitan-version"
