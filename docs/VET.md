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


## sliceset: a slice the package asks membership of

`src/vet/sliceset.go` closes the exit the map check used to leave open. Told
that a `map[K]bool` is a set, the cheapest way out is a `[]K` and
`slices.Contains`, which answers the same question by walking every element
that was ever added. The remedy is the same package, so the check is the same
shape: an org module FAILS, everybody else WARNS (`isOrgModule`).

Three findings:

- a slice literal spelled inside the lookup --
  `slices.Contains([]string{"linux", "darwin"}, name)`. The literal exists for
  that one question and has no other use.
- `if !slices.Contains(s, v) { s = append(s, v) }`. Add-if-absent IS a set
  insert, whatever `s` does afterwards, and it costs a scan per insert. The
  absence test also reads `slices.Index(s, v) < 0` and `== -1`.
- a slice the package creates whose every use is a set operation, and which
  it asks membership of at least once.

That last one is the map rule with slices' vocabulary. The uses it accepts are
`append` back into the same variable, `len`, a value-only `range`, a
comparison against `nil`, `slices.Contains`, and a `slices.Index` compared
against a constant. A membership test is what makes the slice a set: append
and range alone are a list, and a list stays a list.

**Writing the scan out by hand does not escape the check.** A loop over a
candidate whose body is one `if` comparing the element to a value IS
`slices.Contains`, and it counts as the membership read that reports the
slice.

### What keeps a slice a slice

Position and repetition are what a slice has and a set does not, so any use
that could read either one drops the candidate: an index or a slice
expression, a `range` whose key is used, the slice spread into somebody
else's `append`, a `slices.Index` whose result is the answer, and the slice
as an argument, a return, a field or a channel value. `validGOOS` in
`src/cmd/targets.go` is the honest version of that last one: membership
decides the flag, and `strings.Join` renders the error, so the order is part
of what the variable is for.

A parameter is not a candidate either. It arrives from a caller, and what to
store it in is that caller's decision. A `[]byte` is a buffer, and an element
type that is not comparable cannot be a set member.


## The fixer: what the checks prove, they rewrite

`src/vet/setfix.go` turns a reported map or slice into a set. It runs for both
checks, because both name the same remedy.

| before | after |
| --- | --- |
| `make(map[K]bool)`, `map[K]bool{}`, `make([]T, 0)`, `[]T{}` | `set.New[K]()` |
| `map[K]bool{"a": true}`, `[]T{a, b}` | `set.Of[K]("a")`, `set.Of[T](a, b)` |
| `var m map[K]bool` | `var m set.Set[K]` |
| `m[k] = true`, `s = append(s, v)` | `m.Add(k)`, `s.Add(v)` |
| `m[k]`, `slices.Contains(s, v)` | `m.Contains(k)`, `s.Contains(v)` |
| `delete(m, k)`, `clear(m)`, `len(m)` | `m.Remove(k)`, `m.Clear()`, `m.Len()` |
| `for k := range m`, `for _, v := range s` | `for k := range m.All()` |

The type argument is written out. `set.Of(1, 2)` off a `[]float64` literal
would infer `int`, and `set.Of[float64](1, 2)` cannot.

**One use with no spelling blocks the whole variable.** Half a rewrite does not
compile, so the finding stays a diagnostic and the file is untouched. The same
answer covers a `slices.Index` read, a `nil` comparison, and an append whose
result lands somewhere else.

### What the fixer refuses to touch

The rewrite is only safe when this pass sees every use (`setFixable`), so a
package-level variable is fixed only when it is unexported AND its directory
holds no `_test.go` file. An exported one is reachable from another package.
An unexported one is reachable from the package's test files, which the plain
package variant does not hold -- rewriting the declaration from that variant
would leave the test files calling `m[k] = true` on a `Set`. A local variable
is used where it is declared, so it is always fixable.

A file that already binds the name `set` to something else keeps its
diagnostic and loses its fix (`setNameFree`). The fix goes through the same
`Editor` as every other AST fix: it writes locally, and on CI the analyzer's
own diagnostic is what fails the build.


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

## jsoninterp: a JSON document built out of string pieces

`src/vet/jsoninterp.go` reports a JSON document assembled from text rather than
marshaled from a value:

```go
{% raw %}
fmt.Sprintf(`{"sha":%q}`, sha)
`{"sha":"` + sha + `"}`
template.New("body").Parse(`{"name":"{{.Name}}"}`)
{% endraw %}
```

None of the three escapes anything for JSON. A value carrying a quote, a
backslash or a newline does not corrupt the value; it corrupts the DOCUMENT,
and the reader on the far side gets a parse error or a different object than
the one that was sent. A value the user controls chooses that object.

`%q` looks like the careful spelling and is not. It writes GO quoting, which is
a different language: `strconv.Quote` emits escapes such as `\xff` that JSON
has no syntax for, so a value that is not valid UTF-8 produces text no JSON
parser accepts. The one finding this check made on `simple-llm-ui` was a `%q`
in a test that fakes a GitHub repository listing.

The remedy is `encoding/json`. Marshal a struct or a `map[string]any`; a
fragment that is already JSON and must stay raw is a `json.RawMessage` field,
which is exactly the case that type exists for.

### There is no JSON context in any template package

The question this check keeps answering: `html/template` escapes by context, so
is there something equivalent for JSON?

No. There is no `json/template`, and neither template package has a JSON
context.

- **`text/template` escapes nothing at all.** It has no notion of an output
  language, and no reference to `encoding/json` anywhere in the package. Every
  action writes its value verbatim. A JSON template is therefore the
  concatenation case with extra steps.
- **`html/template` escapes for HTML**, and its typed strings are the whole set
  of languages it knows: `HTML`, `HTMLAttr`, `CSS`, `JS`, `JSStr`, `URL` and
  `Srcset` (`content.go`). There is no `JSON` among them, and none for XML
  either.
- The one place the standard library JSON-escapes for a template is
  `jsValEscaper` (`html/template/js.go`), which marshals the value with
  `json.Marshal`. It is reachable only from `stateJS` (`escape.go`) -- that is,
  a value inside a `<script>` element of an HTML document. Handed a bare JSON
  document, `html/template` escapes it as HTML instead, which is a different
  and equally wrong answer: a `<` in your data becomes `&lt;`.

So a template whose text is JSON is reported like the other two shapes. This is
the one place `writeruns` and `jsoninterp` point in opposite directions:
`writeruns` names `text/template` as the remedy for a document written one line
at a time, and that remedy is right for a shell script and wrong for JSON.

### What counts as a document

A finding needs a value entering the text AND text that is JSON.

The value is a format verb (`fmt.Sprintf`, `Fprintf`, `Errorf`, `Printf`,
`Appendf`), an operand of a `+` concatenation that is not a string literal, or
a template action. A doubled `%%` interpolates nothing, and an expression the
type checker folded to a constant carries no runtime value; neither is
reported.

The text is judged by `isJSONDocument` (`src/vet/jsonshape.go`), on the whole
document rather than one piece of it: the literal parts of a concatenation are
joined first, so `` `{"sha":"` + sha + `"}` `` is read as `{"sha":""}` and a
fragment that means nothing alone is still read in place. Two things must hold.

1. **Outside its quoted strings the text spells only JSON syntax** -- the
   punctuation, a number, `true`/`false`/`null`, whitespace, and the holes the
   values fill. This is what keeps prose out: `expected {"ok":true}, got %s`
   has the word `expected` outside every string, so it is a message about JSON
   rather than JSON.
2. **It shows one of three shapes**: a quoted key (a closed string a colon
   follows), an array holding a string or an object, or an object holding a
   string.

The second condition is why `fmt.Sprintf("{%s}", v)` and `fmt.Sprintf("[%s]",
tag)` stay silent. Braces around one value are a notation this check cannot
tell from set notation, a CSS rule or a log tag, and a check that cries wolf on
ordinary formatting is one nobody reads. The cost is that a JSON array of bare
values is missed; the shapes that carry a quote are the ones a value breaks.

### Scope

A document that is one string constant is never reported: it holds no value, so
nothing can break it. Neither is a call this walk cannot read the text of -- a
format string held in a variable, or `template.ParseFiles`, whose document is
in another file.

Only `fmt` is read. A logging call that formats JSON-looking text writes a log
line, and a log line is not a document anybody parses.

The remedy is the standard library, so it costs a consumer no dependency. The
severity is still the split the set checks carry: an org module FAILS
(`isOrgModule`), and everywhere else WARNS. There is no opt-out marker. Every
package variant walks the same file, so warned sites are deduplicated by
`file:line` for one vet run (`resetJSONInterpWarnings`).

## commentnumbers: a number in a comment

`src/vet/commentnumbers.go` reports any number written in a Go comment, in
digits or in words. The remedy it names is always the same: describe what the
code does and let the reader count.

A number in a comment is a count of what exists on the day it was written. The
edit that adds an item does not update it, so the comment quietly goes false,
and the alternative -- hunting down every comment that counted the old set --
is work nobody asked for. Naming the thing instead survives both.

```go
// BAD                                 // GOOD
// The four descriptor probes ...      // The descriptor probes ...
// splits three ways:                  // splits several ways:
// asked once per repository           // asked a single time per repository
// warns at 500 lines, errors at 750   // warns past the warn threshold
// grace = 57.5, effective = 57.5      // the grace floor is what applies
```

### What counts as a number

The check walks each comment's tokens -- runs of letters, digits and the name
characters `_`, `.`, `/`, `:` and `-` -- and reports two shapes:

- **A digit run**, unless it touches a letter or wears an ordinal suffix. So
  `sha256`, `amd64`, `p95`, `10ms` and `wasip1` are names and stay; a bare
  `500`, a `2.5`, and a version literal like `1.24.7` are numbers and go.
- **A whole alphabetic word** naming a number: the cardinals up to `thousand`,
  `million` and `dozen`, the ordinals up to `thousandth`, and
  `once`/`twice`/`thrice`. Case does not matter, so `One` is reported like
  `one`. A word that merely contains one (`someone`, `oneShot`, `atonement`)
  is not a match, because the whole run must be the word.

A token holding `://` is a URL and is skipped whole, so citing an issue by its
full address is how to keep a reference that carries a number. A qualified
name -- a marker strictly between word characters, as in `example.com/mod/v2`,
`net/http` or `sync.Once` -- is a name rather than prose and is left alone. A
compiler directive (`//go:build`, `//go:generate`) is machine text and is never
reported, and a generated file is skipped entirely.

### Scope

A finding is a WARNING, in every module -- unlike the set checks, org code is
not held to a harder severity here. A stale count is prose, not broken code,
so it must not fail a build on its own. They arrive by the dozen though, so
the warnings budget (`docs/WARNINGS-GATE.md`) is what turns a repo full of
them red, and a repo with a handful stays green while its author rewrites
them.

There is no opt-out marker and no module exemption. A warning is spent per
`file:line`, so a sentence naming several numbers costs a single warning and a
package walked under several variants still costs that one
(`resetCommentNumbersWarnings`).
