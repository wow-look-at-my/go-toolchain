# Custom vet checks

Extracted verbatim from CLAUDE.md (1.85x over its 40,000-character budget).

- `src/vet/` — custom vet checks (assert normalization, unused imports, gotest.tools migration, testify import rewrite fork→upstream with vendor resync, the `testifycast` analyzer that inserts explicit type conversions into cross-type `assert`/`require` `Equal`/`NotEqual` operands so they pass against upstream testify — each edit records any import the spelled type needs (`CastEdit.AddImports`) and applying the fix adds it, because the operand's type package isn't necessarily imported by the file: an `os.FileMode` operand yields a cast spelled `fs.FileMode` (io/fs is the alias's origin package), and writing that cast without the `io/fs` import left the file failing to load (`undefined: fs`), which blocked every later vet run including the fixer's own verify re-run, wedging the tree until the import was added by hand; a conversion whose package name is shadowed or taken by a different import at the use site is skipped instead of emitted broken, and the `bannedoutput` analyzer, which bans `fmt.Print*`, `fmt.Fprint*(os.Stdout|os.Stderr)`, and `log.*` calls outside `src/logger/`, `src/cmd/console.go`, and `_test.go` files so all output routes through `src/logger` -- SCOPED to the go-toolchain module via `pass.Module` (vetSemantic loads packages with `packages.NeedModule`): the analyzer also runs on every consumer project go-toolchain builds, and a consumer's `fmt.Println` must never be flagged (no src/logger to route through; an empty module path fails open to checked so the analysistest GOPATH fixtures still exercise the checks). Writers held in variables are deliberately not flagged, the documented escape hatch for load-bearing streams: the watchdog's `origStderr`, the claudeguard abort message, mid-line progress fragments, interactive prompts). **Fix mode vs check mode (the `Editor` abstraction)**: `root.go` reads `os.Getenv("CI")` in exactly one place and builds a single `vet.Editor` (`src/vet/editor.go`); `vetSemantic` threads it through every fixer. An `applyEditor` (local) writes proposed changes; a `checkEditor` (CI) records them as violations and never writes. No fixer branches on the CI flag itself — each computes the canonical bytes for a file and hands them to the editor via `ed.Require(path, want, reason)` (for sole-detector fixers — gofmt, the `wow-look-at-my/testify` fork and `gotest.tools` import migrations, and the `testifycast` casts — whose recorded violation is what fails CI) or `ed.Apply(path, want)` (for fixes that ALSO emit an analyzer diagnostic, so the diagnostic fails CI and the editor only writes locally / no-ops on CI). `ed.Err()` surfaces the accumulated violations (combined with vet diagnostics), each carrying a unified diff (`src/vet/diff.go`, `github.com/pmezard/go-difflib`) from the file's current content to the canonical `want`, computed once at detection time while both are in hand — so a CI failure is answerable from the log alone, no checkout or local `go-toolchain` run needed, which is what makes it legible to an agent with no code-execution capability; `ed.Writes()` gates write-only preconditions like the uncommitted-changes guard. This keeps CI from passing green on a tree the local autofixer would have changed (e.g. a lingering fork import). Any new in-place fixer MUST route its writes through the `Editor` (never a bare `os.WriteFile`), or CI will silently stop enforcing it. **Canonical emission (`src/vet/format.go`)**: gofmt's doc-comment formatter (`go/doc/comment`, since Go 1.19) rewrites a doubled apostrophe into U+201D and a doubled backtick into U+201C inside top-level doc comments, silently corrupting literal author text (e.g. a POSIX single-quote shell escape) and turning an ASCII file multi-byte. `RunGofmt` reverts this via `revertDocCommentSmartQuotes`, which restores the ASCII digraph for every U+201C/U+201D that lands **inside a comment** — located by parsing the gofmt-valid source, so curly quotes inside string/rune literals (real program data, not prose) are never touched, and a fast path skips the parse for files with no curly quotes at all. The revert is **curative, not just preventive**: gofmt is the only thing that produces these runes in Go source and no author types one in a comment by hand, so it also heals comments that an earlier, unfixed run already corrupted — not only the file currently being formatted. Every rewriter that re-emits a file through `go/printer` (the `ASTFixes` apply path, the `testifyimport`/`gotestmigrate`/`unusedimport` import fixers, and testifycast's import-adding path — `addImportsToSource`, taken only when an edit recorded a missing import; the plain surgical-byte-edit path never reprints) routes its bytes through `canonicalizeGoSource`, which reruns `go/format` so the output is gofmt-canonical (tabs to indent, spaces to align — `go/printer`'s default mode tab-aligns *both*) and then applies the same revert. Any new rewriter that prints a modified AST MUST emit through `canonicalizeGoSource`, or it will tab-align its output and corrupt comment quotes. The uncommitted-changes guard's go-git backend lives in `gogit.go` (`!cosmo`; go-git's go-billy/osfs needs x/sys/unix, which has no cosmo port) — `gogit_cosmo.go` stubs it so `checkFileCommittedByName` always takes the git-CLI fallback under cosmo; that same fallback is also what supports `feature.manyFiles`/`index.skipHash` repos, whose zero-hash index trailer (git >= 2.40) go-git v5 rejects with "invalid checksum" (regression-tested in vet_semantic_test.go, which also runs its test-repo git commands hermetically so host config can't leak in).

## Which packages a vet run actually loads

`src/buildtags` derives the build-tag configurations vet runs under, and two
kinds of identifier must never become one:

- **A nested module is not this module's code.** `Scan` stops at any directory
  holding its own `go.mod`. A pattern naming one fails to load outright ("main
  module does not contain package ..."), and its tags belong to its own
  pipeline, not a configuration this module can be asked to vet itself under.
- **`cosmo` names a build target.** It is the gosmopolitan fork's GOOS, so it is
  absent from the `go tool dist list` values `knownOS` was built from. Under
  `-tags cosmo` on a normal host every `_linux.go` filename constraint still
  holds, so each cosmo variant collides with its linux sibling
  (`socketPeerPID redeclared`). The `GOOS=cosmo` matrix job checks those files.

`packages.Config.Tests` then loads each package up to four ways: plain, the same
code recompiled with its internal `_test.go` files, the external `_test`
package, and the generated test main. The plain variant holds none of the test
files, so **`deadcode` is answered by the richest variant of each package path**
(`richestVariants`, `src/vet/loadvariants.go`). Reading the plain one instead
made every unexported helper that only a test calls a violation, and reported a
genuinely dead one once per variant.

`ParseFile` runs on one goroutine per file, so the record of what was parsed is
behind a mutex (`parseRecorder`). Unlocked, a module this size died with
`fatal error: concurrent map writes`, and the output watchdog's pipes swallowed
the trace — CI saw a bare `exit status 2` with nothing above it.


## mapset: a map[K]bool written where a set is meant

`src/vet/mapset.go` reports the map that picks the wrong default and offers
[`github.com/wow-look-at-my/go-containers/set`](https://github.com/wow-look-at-my/go-containers/tree/master/set)
in its place. Two shapes FAIL the build:

- a `map[K]bool` composite literal whose every value is the constant `true`.
- a `map[K]bool` variable made empty -- `make(map[K]bool)`, `map[K]bool{}`,
  or a bare `var` -- that the package writes `true` into, and never uses in a
  way that could read a real boolean.

A `map[K]struct{}` gets a WARNING instead, never a diagnostic. That map
already carries no value, so which of the two to write is the author's call;
the warning names `set.Set` once per site and counts against the warnings
budget. Every package variant walks the same file, so the sites are
deduplicated by `file:line` for the length of one vet run
(`resetMapSetWarnings`).

The `set` package itself is exempt from that warning, under its own path and
its `_test` variant (`isSetPackage`). `Set[T]` IS the `map[T]struct{}` the
warning points at, and its eight storage sites would spend half the warnings
budget telling the remedy to use itself.

### What disqualifies a made-empty bool map

The use walk (`mapSetUses`) accepts a write of `true`, `delete`, `clear`,
`len`, a key-only `range`, and an index read. Anything else means the map is
not provably a set, and the candidate is dropped:

- `v, ok := m[k]` -- present-and-false is not the same as absent.
- `m[k] = <anything but true>`, and any compound assignment.
- `for k, v := range m`, where `v` is used.
- the map as a value: an argument to anything but those three builtins, a
  return, a struct field, a channel send, `&m`. Past that point the deciding
  use can live in another package, where this walk cannot follow.

A literal carrying one `false` is a lookup table and stays.
`buildtags.platformIdents` is exactly that: it answers "is this a platform
ident" and writes `"ignore": false` to say no.

### Severity, not carve-outs

The check runs on every module go-toolchain builds, its own and every
consumer's: a map that carries no information is wasteful wherever it is
written. What the module decides is the severity, not whether the finding
appears.

In a `github.com/wow-look-at-my/` or `github.com/PazerOP/` module
(`isOrgModule`) the `map[K]bool` findings FAIL the build -- the remedy is one
first-party require away. In anybody else's module the same findings are
warnings: the code is just as wasteful, but the fix would add a dependency its
author never chose, and that is theirs to decide. A driver that supplies no
module info fails open to org, so the analysistest fixtures still expect
diagnostics.

There is no opt-out marker. Every shape the check reports is a set by
construction, so a suppression comment could only ever hide one -- and the two
rules already leave a real map alone: write one `false`, read with `v, ok :=`,
or hand the map to another function, and nothing fires.


## writeruns: a document spelled one write at a time

`src/vet/writeruns.go` reports the shape this repo wrote in five places before
the check existed:

```go
fmt.Fprintf(script, "  c=%s/$k\n", apeRunDir)
script.WriteString("  p=\"$c/${0##*/}\"\n")
script.WriteString("  if [ ! -x \"$p\" ]; then\n")
script.WriteString("    (umask 077; mkdir -p \"$c\") || exit 121\n")
script.WriteString("    cp \"$o\" \"$p.$$\" || exit 121\n")
```

That is one shell script, and nothing in the source shows its shape. The
escapes, the trailing `\n` and the writer's name stand between the reader and
every line of it. Whoever edits it reads Go to find the line they want, and
whoever reviews the diff cannot see the output change.

The first two writes of a run are free. The third and each one after it gets
one warning, at its own line. So the five statements above spend three
warnings, and a pair of writes spends none.

The check WARNS; it never fails a build by itself. A long enough run still
fails the run through the warnings budget (`docs/WARNINGS-GATE.md`), which is
what this repo's 25-write mermaid header did.

### What the remedy is

The finding ends when the document becomes one piece of text.

- With values in it, that is a `text/template`. `src/summary/gantt.go` renders
  the whole chart from one template, and `src/cmd/claudeguard.go` renders the
  abort message from another, deleted-outputs list and all.
- With no values in it, one string constant IS the document, and a single
  write of it ends the run. `src/hostos/detection.go` holds its banner that
  way. Note a raw string cannot hold text that quotes shell or markdown with
  backticks, which is why both templates here are interpreted strings joined
  by `+`.

A refactor of this kind must not move a byte of the output. Both documents are
pinned by an equality test -- `TestRenderGanttRendersTheWholeDocument` and
`TestAgentOutputMessageRendersTheWholeDocument` -- because the `Contains`
assertions that surrounded them pass on a message whose blank lines moved.

### What counts as a write

A statement joins a run when its result is dropped (it is an expression
statement), its writer is one this can name (`w`, `s.buf`), and its text is
spelled in the source as a string or character literal.
`fmt.Fprint`/`Fprintf`/`Fprintln` and `io.WriteString` name the writer first;
`Write`, `WriteString`, `WriteByte` and `WriteRune` name it as the receiver.

Three things end a run: any other statement, a write to a different writer, and
a write whose text is computed. `b.WriteByte(c)` inside an escaper writes one
byte of a value, and no template renders it.

A writer that digests its input never counts, whatever it is handed
(`isHashWriter`: a type carrying both `Sum` and `BlockSize`).
`computeFingerprint` writes four framed lines into a `sha256.New()`, and
rendering those through a template would change the digest and help nobody.

### Scope

The check runs on every module go-toolchain builds. `text/template` is in the
standard library, so unlike `mapset`'s remedy it costs a consumer no
dependency, and the severity is the same in every module: a warning.

There is no opt-out marker. Every package variant walks the same file, so the
sites are deduplicated by `file:line` for the length of one vet run
(`resetWriteRunWarnings`).
