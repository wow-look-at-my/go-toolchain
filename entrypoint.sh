#!/bin/sh
set -e

MIN_COVERAGE="$1"
OUTPUT="$2"
COV_DETAIL="$3"
JSON_OUTPUT="$4"
VERBOSE="$5"
WORKING_DIR="$6"

# Change to working directory
cd "$GITHUB_WORKSPACE/$WORKING_DIR"

# Build command arguments
ARGS=""

if [ -n "$MIN_COVERAGE" ] && [ "$MIN_COVERAGE" != "" ]; then
    ARGS="$ARGS --min-coverage $MIN_COVERAGE"
fi

if [ -n "$OUTPUT" ] && [ "$OUTPUT" != "" ]; then
    ARGS="$ARGS -o $OUTPUT"
fi

if [ -n "$COV_DETAIL" ] && [ "$COV_DETAIL" != "" ]; then
    ARGS="$ARGS --cov-detail $COV_DETAIL"
fi

if [ "$JSON_OUTPUT" = "true" ]; then
    ARGS="$ARGS --json"
fi

if [ "$VERBOSE" = "true" ]; then
    ARGS="$ARGS --verbose"
fi

# Run go-safe-build
# shellcheck disable=SC2086
exec go-safe-build $ARGS
