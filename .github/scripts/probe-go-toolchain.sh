#!/bin/bash
# usage: probe-go-toolchain.sh <go-toolchain binary> <scratch dir> Exits 0
# when the binary runs and the go command it links as "go" runs too. A release
# can pass "version" and still ship a go command that cannot run.
set -uo pipefail
bin="$1"
dir="$2"
mkdir -p "$dir"
probe_go="$dir/go"
if [ "${RUNNER_OS:-}" = Windows ]; then
	probe_go="$dir/go.exe"
fi
ln -sf "$bin" "$probe_go"
"$bin" version 2>&1 || exit 1
env -u GOROOT "$probe_go" version 2>&1 || exit 1
# A release stamped after its GOSTDEMB record answers version and still cannot find its GOROOT.
env -u GOROOT GOTOOLCHAIN=local GOTELEMETRY=off "$probe_go" env GOROOT 2>&1 || exit 1
