# The comment scan: a number in a comment

`src/cmd/slopfmtphase.go` reports any number written in a comment, in digits or in words. The remedy it names is always the same: describe what the code does and let the reader count.

A number in a comment is a count of what exists on the day it was written. The edit that adds an item does not update it. So the comment quietly goes false, and the alternative. Naming the thing instead survives both.

```go
// BAD                                 // GOOD
// The four descriptor probes ...      // The descriptor probes ...
// splits three ways:                  // splits several ways:
// asked once per repository           // asked a single time per repository
// warns at 500 lines, errors at 750   // warns past the warn threshold
// grace = 57.5, effective = 57.5      // the grace floor is what applies
```

## Why it is a phase and not an analyzer

The rule was a vet analyzer, `src/vet/commentnumbers.go`. An analyzer runs on `*ast.File` values. `go/packages` produces those only after it resolves every import, reads every dependency's export data and type-checks the module. That is minutes of work before the first comment is read. None of it answers the question. A comment is bytes.

The rule now lives in [`slopfix`](https://github.com/wow-look-at-my/slopfix) and runs as the first phase of the pipeline, ahead of the dependency check, `go mod tidy` and vet. Two things follow.

It answers on a tree that does not build. A missing import, an unresolvable module, a syntax error in another package: none of them stop the report, because nothing here parses the language.

It answers for every language. slopfix parses each file with the grammar its extension names. So a shell script, a workflow, a Rust file and a TypeScript file are all scanned. The analyzer only ever saw Go, and the stale prose in a `run:` script was never anybody's finding.

## Why it runs a binary

slopfix is not importable. It parses with tree-sitter, and it generates each grammar's parse table at build time. It commits none of that. So every grammar package is empty on a fresh checkout, and no module can require it. The published binary is what the slopfix README hands a consumer.

`ensureSlopfix` resolves one: `GO_TOOLCHAIN_SLOPFIX_BIN` names a local build, otherwise the pipeline downloads the host's slot from buildhost into the go cache and keeps it. `GO_TOOLCHAIN_SLOPFIX_VERSION` pins a release.

A failure to resolve fails the phase, and the pipeline with it. A comment scan that never read a comment must not report a clean tree.

## What is scanned

The walk starts at the repository root, not at a module. It skips a hidden directory, `vendor`, `node_modules`, `testdata`, the build output directory, and any file above a megabyte.

It skips a nested module too, whose text belongs to that module. The exception is a root that is not itself a module: skipping there scans nothing, because the repository's modules all sit below the root.

A file whose extension names no grammar is skipped rather than guessed at. slopfix reads a file it is handed by name whatever the extension. So the walk is what holds that line. A wrong guess reports a string literal as prose, and a rule nobody trusts is a rule nobody keeps.

## What counts as a number

The check walks each comment's tokens -- runs of letters, digits and the name characters `_`, `.`, `/`, `:` and `-` -- and reports two shapes:

- **A digit run**, unless it touches a letter or wears an ordinal suffix. So `sha256`, `amd64`, `p95`, `10ms` and `wasip1` are names and stay. A bare `500`, a `2.5`, and a version literal like `1.24.7` are numbers and go.
- **A whole alphabetic word** naming a number: the cardinals up to `thousand`, `million` and `dozen`, the ordinals up to `thousandth`, and `once`/`twice`/`thrice`. Case does not matter, so `One` is reported like `one`. A word that merely contains one (`someone`, `oneShot`, `atonement`) is not a match, because the whole run must be the word.

A number behind a section sign is exempt. `§7.3` and `§ 4` cite a section of a document, and the sign is the spelling a reader looks it up by. It is the escape hatch for a document that publishes no slug -- the sign covers only the number it introduces.

An HTTP status code is exempt, but only when the word `HTTP` (in any case) sits immediately before it. `HTTP 403` names a protocol answer that no edit changes, while a bare `403` is the shape of a line number or a row count. The exemption covers a status-code-width run of digits and nothing else, so `HTTP 4 retries` is a count and goes.

A sum of money is exempt. A currency sign directly against the digits makes the token an amount, which states what something costs rather than counting what is below. Only the amount goes free, so `$1 is the boundary, and 4 dp under it` still reports the `4`, and `costs $ 5` reports.

A token holding `://` is a URL and is skipped whole. So citing an issue by its full address is how to keep a reference that carries a number. A qualified name -- a marker strictly between word characters, as in `example.com/mod/v2`, `net/http` or `sync.Once` -- is a name rather than prose and is left alone. A compiler directive (`//go:build`, `//go:generate`) is machine text and is never reported. And a generated file is skipped entirely.

## Scope

A finding is a WARNING, in every module -- unlike the set checks in [VET.md](VET.md), org code is not held to a harder severity here. A stale count is prose, not broken code. So it must not fail a build on its own. They arrive by the dozen though. So the warnings budget ([WARNINGS-GATE.md](WARNINGS-GATE.md)) is what turns a repo full of them red.

There is no opt-out marker and no module exemption. A warning is spent per `file:line`. So a sentence naming several numbers costs a single warning: the repair is a rewrite of the line, whatever it counts.
