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

The rule now lives in [`slopfix/commentnumbers`](https://github.com/wow-look-at-my/slopfix/tree/master/commentnumbers) and runs as the first phase of the pipeline, ahead of the dependency check, `go mod tidy` and vet. Two things follow.

It answers on a tree that does not build. A missing import, an unresolvable module, a syntax error in another package: none of them stop the report, because nothing here parses the language.

It answers for every language. `commentnumbers` reads a comment by its delimiters rather than by a grammar. So a shell script, a workflow, a Dockerfile, a Rust file and a TypeScript file are all scanned. The analyzer only ever saw Go, and the stale prose in a `run:` script was never anybody's finding.

## What is scanned

The walk starts at the repository root, not at a module. It skips a hidden directory, `vendor`, `node_modules`, `testdata`, the build output directory, and any file above a megabyte.

It skips a nested module too, whose text belongs to that module. The exception is a root that is not itself a module: skipping there scans nothing, because the repository's modules all sit below the root.

It skips a git submodule on the same ground. This one carries no exception. A submodule's working tree is another repository's checkout. That repository writes the prose and takes the fix. Nothing here can repair a finding inside it. Git marks such a tree by writing `.git` as a FILE. The file holds a gitdir pointer, where an ordinary checkout keeps a directory. The skip reads that marker rather than a name. The nested-module predicate cannot stand in for it, because that one reads `go.mod`. A submodule of C, C++ or Rust carries none. A vendored driver or compiler tree is also where the findings run away with the whole warnings budget.

A file whose extension `commentnumbers` has no comment syntax for is skipped rather than guessed at. A wrong guess reports a string literal as prose, and a rule nobody trusts is a rule nobody keeps.

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
