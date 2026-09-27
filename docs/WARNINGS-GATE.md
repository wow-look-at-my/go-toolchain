# Warnings budget

`src/logger/warncount.go` + `src/cmd/warningsgate.go`.

Every Warn/WarnFile actually emitted (post level-filtering) is recorded process-wide, and `checkWarningsGate` fails the run with `build failed: N distinct warnings emitted (threshold: 15)` when the count exceeds `maxWarnings` (a constant 15 — deliberately no flag or env knob). `logger.WarnCount` is the DISTINCT count the gate reads, `logger.TotalWarnCount` is every emission, and `ResetWarnCount` clears both (for tests, which share one process). The gate runs at the very END of the pipeline commands — the root run (before `saveFingerprint`, so a gate-failed run is never stamped up-to-date) and matrix `runRelease` — so every warning prints before the failure.

## The budget counts DISTINCT warnings

Two emissions are the same warning when the recorded text is identical. And the budget counts that warning once. One root cause repeats once per file, per module, per package variant or per retry. The commonest repeat is structural: vet's auto-fixer rewrites the tree, and `runWithRunnerOnce` then re-runs the whole pipeline against the corrected code. So every warning of the first pass is emitted a second time. Before the fold, that alone doubled a dirty tree's count and can fail a run that a clean tree passed.

What stays distinct: `WarnFile` records the `<file>: ` prefix. So the same sentence about two files is two warnings, and a message naming the value it found keeps its own identity. What folds: byte-identical text, however far apart the two emissions are.

Folding governs the COUNT only. Every warning still prints or annotates as it did. And the recap names the repeat count of each (`(emitted 5 times)`) beside a total. So a folded repeat is visible rather than hidden:

```
build failed: 17 distinct warnings emitted (threshold: 15), 34 emitted in total (a repeat counts once).
```

An analyzer that deduplicates its own findings (`mapset`, `writeruns`, both keyed on `file:line`) is doing a different job. It keeps the site from PRINTING four times as go/packages loads a package four ways. That one is about the log. This one is about the budget.

## The failure re-prints what it counted

A bare count is unactionable: the reader scrolls back through thousands of lines and guesses which output was to blame. So the gate prints a numbered recap of every counted warning, in emission order, at the point of failure:

```
build failed: 17 distinct warnings emitted (threshold: 15), 34 emitted in total (a repeat counts once). The warnings, in the order they were first emitted:
   1. cache: GO_BUILDCACHE_CONFIG: deprecated S3-style field(s): key_id ... (emitted 2 times)
   2. cache: web index fetch failed: ... (emitted 2 times)
   ...
```

Routing:

- **Local**: one red block on stderr (`logError` → `logger.Error`).
- **GitHub Actions**: ONE `::error` annotation carrying the whole list — `gha.go` escapes the newlines (`%0A`). So the annotation keeps every line instead of truncating to the first. It is a single annotation on purpose: the individual warnings were already `::warning` annotations, and re-emitting them per line will double the run's annotation count.
- **`--json`**: the recap goes to `rawStderr` (the documented logger bypass), because stdout carries the JSON payload.

The returned error is the one-line `build failed: N distinct warnings emitted (threshold: 15)`, so the process's final error line names one number.

`logger.EmittedWarnings` retains the first `logger.MaxRecordedWarnings` (200) DISTINCT warnings, each with its repeat count. The distinct count itself is unbounded. And the recap reports the difference explicitly (`... and N more (only the first 200 are recorded)`) rather than silently truncating. The deduplication set outlives that retention cap, so a repeat of an unretained warning still folds instead of inflating the count. `WarnFile` messages are retained with their `file: ` prefix. So the recap keeps the annotation's location.

## What does NOT count

Only `src/logger`'s `Warn`/`WarnFile` reach the counters. In particular the output watchdog's `STALLED: no output for Ns` banner does not: it is written with a raw `fmt.Fprintf(w.origStderr, ...)` (`src/cmd/watchdog.go`) that deliberately bypasses the logger. Loud, red, `⚠`-prefixed, and uncounted.

A run that fails the gate while the log is full of STALLED lines usually has a shared root cause rather than a causal link. The recap exists so that distinction never has to be guessed.
