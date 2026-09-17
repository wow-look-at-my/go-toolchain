#!/usr/bin/env bash
# The whole workflow has 15 minutes from the moment the run started. This
# prints the whole minutes left (at least 1, so a job timeout stays valid),
# writes it to the job's outputs when GITHUB_OUTPUT is set, and fails when the
# deadline has passed, so a job that starts late fails before it does work.
set -euo pipefail

budget_minutes=15
started=$(gh api "repos/${GITHUB_REPOSITORY}/actions/runs/${GITHUB_RUN_ID}" --jq .run_started_at)
started_s=$(date -u -d "$started" +%s 2>/dev/null || date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$started" +%s)
now_s=$(date -u +%s)
elapsed_s=$(( now_s - started_s ))
left_s=$(( budget_minutes * 60 - elapsed_s ))
left_m=$(( left_s / 60 ))
if [ "$left_m" -lt 1 ]; then
  left_m=1
fi
echo "run started $started; $(( elapsed_s / 60 ))m$(( elapsed_s % 60 ))s elapsed; ${left_m} whole minutes of the ${budget_minutes}-minute budget left"
if [ -n "${GITHUB_OUTPUT:-}" ]; then
  echo "minutes_left=${left_m}" >> "$GITHUB_OUTPUT"
fi
if [ "$left_s" -le 0 ]; then
  echo "::error::the run passed its ${budget_minutes}-minute budget ${elapsed_s}s after it started"
  exit 1
fi
