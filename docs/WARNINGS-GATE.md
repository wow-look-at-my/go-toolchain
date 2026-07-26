# Warnings budget

`src/logger/warncount.go` + `src/cmd/warningsgate.go`.

Every Warn/WarnFile actually emitted (post level-filtering) increments a
process-wide atomic counter (`logger.WarnCount`; `ResetWarnCount` for tests,
which share one process), and `checkWarningsGate` fails the run with
`build failed: N warnings emitted (threshold: 15)` when the count exceeds
`maxWarnings` (a constant 15 — deliberately no flag or env knob). The gate runs
at the very END of the pipeline commands — the root run (before
`saveFingerprint`, so a gate-failed run is never stamped up-to-date) and matrix
`runRelease` — so every warning prints before the failure; non-pipeline
subcommands (version, install, cacheprog — a separate process whose warnings
never reach the counter — and the `release` tag-and-push flow) are deliberately
not gated.

## The failure re-prints what it counted

A bare count is unactionable: the reader scrolls back through thousands of
lines and guesses which output was to blame — and the guess is usually wrong,
because the loudest lines in a build log are not the counted ones (see below).
So the gate prints a numbered recap of every counted warning, in emission
order, at the point of failure:

```
build failed: 17 warnings emitted (threshold: 15). The warnings, in the order they were emitted:
   1. cache: GO_BUILDCACHE_CONFIG: deprecated S3-style field(s): key_id ...
   2. cache: web index fetch failed: ...
   ...
```

Routing:

- **Local**: one red block on stderr (`logError` → `logger.Error`).
- **GitHub Actions**: ONE `::error` annotation carrying the whole list —
  `gha.go` escapes the newlines (`%0A`), so the annotation keeps every line
  instead of truncating to the first. It is a single annotation on purpose:
  the individual warnings were already `::warning` annotations, and re-emitting
  them per line would double the run's annotation count.
- **`--json`**: the recap goes to `rawStderr` (the documented logger bypass),
  because stdout carries the JSON payload.

The returned error stays the one-line `build failed: N warnings emitted
(threshold: 15)`, so the process's final error line is unchanged.

`logger.EmittedWarnings` retains message text for at most
`logger.MaxRecordedWarnings` (200) warnings; the counter is unbounded, and the
recap reports the difference explicitly (`... and N more (only the first 200
are recorded)`) rather than silently truncating. `WarnFile` messages are
retained with their `file: ` prefix, so the recap keeps the annotation's
location.

## What does NOT count

Only `src/logger`'s `Warn`/`WarnFile` increment the counter. In particular the
output watchdog's `STALLED: no output for Ns` banner does not: it is written
with a raw `fmt.Fprintf(w.origStderr, ...)` (`src/cmd/watchdog.go`) that
deliberately bypasses the logger, because the logger writes to the current
`os.Stderr` — which is the watchdog's own monitored pipe, so routing the
warning there would feed it back into `forward()` and reset the stall timer.
Loud, red, `⚠`-prefixed, and uncounted. Likewise, a warning suppressed by
`--log-level error/silent` is never counted (the budget gates what the user
actually saw), and the `cacheprog` subprocess is a separate process whose
warnings never reach this counter.

A run that fails the gate while the log is full of STALLED lines usually has a
shared root cause rather than a causal link — e.g. a slow cache-index fetch
both emits the real warnings (`src/cache/web_index.go`'s degradation cascade)
and makes the pipeline quiet enough for the watchdog to fire. The recap exists
so that distinction never has to be guessed.
