#!/usr/bin/env bash
# Builds commands from a GitHub checkout of a Go module, with no request to golang.org.
set -euo pipefail

go_cmd=$1
repo=$2
ref=$3
out=$4
shift 4

work=$(mktemp -d "${RUNNER_TEMP:-/tmp}/build-from-github.XXXXXX")
if [[ -n "$ref" ]]; then
	git clone --quiet --depth 1 --branch "$ref" "$repo" "$work"
else
	git clone --quiet --depth 1 "$repo" "$work"
fi
mkdir -p "$out"

# The jq program maps each module path to its GitHub path, and drops a path it cannot map.
replaces=$("$go_cmd" -C "$work" mod edit -json | jq -r '
	def github:
		if startswith("golang.org/x/") then "github.com/golang/" + ltrimstr("golang.org/x/")
		elif startswith("google.golang.org/protobuf") then "github.com/protocolbuffers/protobuf-go" + ltrimstr("google.golang.org/protobuf")
		elif startswith("google.golang.org/grpc") then "github.com/grpc/grpc-go" + ltrimstr("google.golang.org/grpc")
		elif startswith("google.golang.org/genproto") then "github.com/googleapis/go-genproto" + ltrimstr("google.golang.org/genproto")
		elif startswith("google.golang.org/api") then "github.com/googleapis/google-api-go-client" + ltrimstr("google.golang.org/api")
		elif startswith("google.golang.org/appengine") then "github.com/golang/appengine" + ltrimstr("google.golang.org/appengine")
		else empty end;
	(.Require // [])[] | select(.Path | test("^(google\\.)?golang\\.org/")) |
	"-replace=\(.Path)=\(.Path | github)@\(.Version)"')

unmapped=$("$go_cmd" -C "$work" mod edit -json | jq -r '(.Require // [])[] | .Path | select(test("^google\\.golang\\.org/") and (test("^google\\.golang\\.org/(protobuf|grpc|genproto|api|appengine)(/|$)") | not))')
if [[ -n "$unmapped" ]]; then
	echo "build-from-github: no GitHub repository is known for: $unmapped" >&2
	exit 1
fi

if [[ -n "$replaces" ]]; then
	replace_args=()
	while IFS= read -r line; do
		replace_args+=("$line")
	done <<< "$replaces"
	"$go_cmd" -C "$work" mod edit "${replace_args[@]}"
fi

for pkg in "$@"; do
	GOFLAGS=-mod=mod GOTOOLCHAIN=local GOTELEMETRY=off GOSUMDB=off GONOSUMDB='*' \
		"$go_cmd" -C "$work" build -o "$out/$(basename "$pkg")" "./$pkg"
done
