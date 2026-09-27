# The prose repair: every slopfix rule over the whole tree

`src/cmd/repairphase.go` is a start and a join around `slopfix.FixTree`. It is the central "repair every file under this tree" call, so a rule slopfix gains later arrives here with no change on this side. Nothing about prose is decided in go-toolchain.

The sweep takes every rule slopfix carries: comment length, a number said in words or digits, the tombstones, the hand-wrapped paragraph, and the STE wording. A rule that only warns leaves the author work its own repair already knows how to do, so the phase repairs first and reports what is left.

A number in a comment is a count of what exists on the day it was written. The edit that adds an item does not update it, so the comment goes false while reading as fact. Naming the thing instead survives the edit.

```go
// BAD                                 // GOOD
// The four descriptor probes ...      // The descriptor probes ...
// splits three ways:                  // splits several ways:
// warns at 500 lines, errors at 750   // warns past the warn threshold
```

## Why it is a phase and not an analyzer

The number rule was a vet analyzer, `src/vet/commentnumbers.go`. An analyzer runs on `*ast.File` values. `go/packages` produces those only after it resolves every import, reads every dependency's export data and type-checks the module. That is minutes of work before the first comment is read, and none of it answers the question. A comment is bytes.

The rules live in [slopfix](https://github.com/wow-look-at-my/slopfix) and run beside the dependency check, `go mod tidy` and `go generate`, ahead of vet. Two things follow.

It answers on a tree that does not build. A missing import, an unresolvable module, a syntax error in another package: none of them stop the report, because nothing here parses the language.

It answers for every language. slopfix reads a comment through go-tree-sitter, so a shell script, a workflow, a Dockerfile, a Rust file and a TypeScript file are all read. The analyzer only ever saw Go, and the stale prose in a `run:` script was never anybody's finding.

## Where it runs, and when

It starts only once `findGoModules` has answered. A repair is a write, and a run begun in a directory that is not a module has no business rewriting whatever prose it finds there. A tree carrying `dats/` suites and no `go.mod` therefore gets no sweep.

It runs on its own goroutine, beside the dependency resolution, `go mod tidy` and `go generate`. Each repaired file is renamed into place, so a reader beside the sweep sees a whole file either way. The test phase joins the sweep before vet, which rewrites the same files. The up-to-date path joins it before the build.

A finding it cannot repair is a defect in slopfix rather than a message for the author. So the phase warns only where a rule and its repair have come apart.

## What is swept

The walk is slopfix's, in `commentfix.TreeFilesMatching` under `slopfix.Reads`. The `slopfix check` command reads the same list. It starts at the repository root, not at a module. It skips a hidden directory, `vendor`, `node_modules`, `testdata`, `build`, and any file above a megabyte.

It skips a nested module too, whose text belongs to that module. The exception is a root that is not itself a module: skipping there sweeps nothing, because the repository's modules all sit below the root.

It skips a git submodule on the same ground, and this one carries no exception. A submodule's working tree is another repository's checkout. That repository writes the prose and takes the fix, and nothing here can repair a finding inside it. Git marks such a tree by writing `.git` as a FILE holding a gitdir pointer, where an ordinary checkout keeps a directory. The skip reads that marker rather than a name. The nested-module predicate cannot stand in for it, because that one reads `go.mod`, and a submodule of C, C++ or Rust carries none. A vendored driver or compiler tree is also where the findings run away with the whole warnings budget.

A file whose extension slopfix has no comment syntax for is skipped rather than guessed at. A wrong guess reports a string literal as prose, and a rule nobody trusts is a rule nobody keeps.

## What counts as a number

slopfix walks each comment's tokens -- runs of letters, digits and the name characters `_`, `.`, `/`, `:` and `-` -- and reports two shapes:

- **A digit run**, unless it touches a letter or wears an ordinal suffix. So `sha256`, `amd64`, `p95`, `10ms` and `wasip1` are names and stay. A bare `500`, a `2.5`, and a version literal like `1.24.7` are numbers and go.
- **A whole alphabetic word** naming a number: the cardinals up to `thousand`, `million` and `dozen`, the ordinals up to `thousandth`, and `once`/`twice`/`thrice`. Case does not matter, so `One` is reported like `one`. A word that merely contains one (`someone`, `oneShot`, `atonement`) is not a match, because the whole run must be the word.

A number behind a section sign is exempt. `SS 7.3` cites a section of a document, and the sign is the spelling a reader looks it up by. It is the escape hatch for a document that publishes no slug, and it covers only the number it introduces.

An HTTP status code is exempt, but only when the word `HTTP` (in any case) sits immediately before it. `HTTP 403` names a protocol answer that no edit changes, while a bare `403` is the shape of a line number or a row count. The exemption covers a status-code-width run of digits and nothing else, so `HTTP 4 retries` is a count and goes.

A sum of money is exempt. A currency sign directly against the digits makes the token an amount, which states what something costs rather than counting what is below.

A token holding `://` is a URL and is skipped whole, so citing an issue by its full address keeps a reference that carries a number. A qualified name -- a marker strictly between word characters, as in `example.com/mod/v2`, `net/http` or `sync.Once` -- is a name rather than prose and is left alone. A compiler directive (`//go:build`, `//go:generate`) is machine text and is never reported. A generated file is skipped entirely.

## Scope

A finding is a WARNING, in every module: unlike the set checks in [VET.md](VET.md), org code is not held to a harder severity here. Stale prose is not broken code, so it must not fail a build on its own. Findings arrive by the dozen though, so the warnings budget ([WARNINGS-GATE.md](WARNINGS-GATE.md)) is what turns a repository full of them red.

There is no opt-out marker and no module exemption. A warning is spent per `file:line`, so a sentence naming several numbers costs a single warning: the repair is a rewrite of the line, whatever it counts.
