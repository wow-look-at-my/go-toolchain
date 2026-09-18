package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Slash-spelled, as goModCache normalizes every path this code compares.
const cacheRoot = "/gomodcache"

// under names a path inside the cache.
func under(parts ...string) string { return cacheRoot + "/" + strings.Join(parts, "/") }

// A cached directory names the module and the version together. The clone needs
// them apart: the path is the repository to fetch, and the version asks the go
// command for the commit.
func TestSplitVersionSeparatesTheModuleFromItsVersion(t *testing.T) {
	path, version := splitVersion(under("github.com/wow/slopfix@v0.0.0-20260912-abc"))
	assert.Equal(t, under("github.com/wow/slopfix"), path)
	assert.Equal(t, "v0.0.0-20260912-abc", version)

	path, version = splitVersion("/home/user/checkout")
	assert.Empty(t, path)
	assert.Empty(t, version)
}

// The approval hash reads the label, and the label carries no version: a
// dependency bump that changed no directive must not demand a fresh approval.
func TestCacheLabelDropsTheVersion(t *testing.T) {
	got := cacheLabel(cacheRoot, under("github.com/wow/slopfix@v1.2.3/grammars/bash/gen.go"))
	assert.Equal(t, "github.com/wow/slopfix/grammars/bash/gen.go", got)
}

// dirAt is a dependency directive in the cached copy of dep at version v.
func dirAt(v string, line int, command string) generateDirective {
	file := under("github.com/wow/dep@" + v + "/g/gen.go")
	return generateDirective{File: file, Line: line, Command: command, Label: cacheLabel(cacheRoot, file)}
}

// Versions of the same dependency hash alike while their commands agree, and
// differently as soon as a command changes.
func TestTheApprovalHashIgnoresAVersionBump(t *testing.T) {
	const cmd = "go run tool -out parser.go in.c"
	v1 := depApprovalHash([]generateDirective{dirAt("v1.0.0", 3, cmd)})

	assert.Equal(t, v1, depApprovalHash([]generateDirective{dirAt("v2.0.0", 3, cmd)}))

	changed := dirAt("v2.0.0", 3, "go run tool -out parser.go other.c")
	assert.NotEqual(t, v1, depApprovalHash([]generateDirective{changed}))
}

// A bump that adds a comment above a directive moves its line and not its
// command, so the recorded approval still holds.
func TestTheApprovalHashIgnoresALineMove(t *testing.T) {
	const cmd = "go run tool -out parser.go in.c"
	assert.Equal(t,
		depApprovalHash([]generateDirective{dirAt("v1.0.0", 6, cmd)}),
		depApprovalHash([]generateDirective{dirAt("v2.0.0", 12, cmd)}))
}

// Reordering the directives of a file changes the order they run in, so it moves the hash.
func TestTheApprovalHashReadsTheOrderOfAFile(t *testing.T) {
	a, b := "go run a -out a.go in.c", "go run b -out b.go in.c"
	assert.NotEqual(t,
		depApprovalHash([]generateDirective{dirAt("v1", 1, a), dirAt("v1", 2, b)}),
		depApprovalHash([]generateDirective{dirAt("v1", 1, b), dirAt("v1", 2, a)}))
}

// A directive naming no output never runs here, so it is no part of the consent.
func TestADirectiveNamingNoOutputIsNotHashed(t *testing.T) {
	translate := dirAt("v1", 8, "go run tool -out parser.go in.c")
	fetch := dirAt("v1", 7, "go run fetch -dir testdata/src")
	assert.Equal(t,
		depApprovalHash([]generateDirective{translate}),
		depApprovalHash([]generateDirective{fetch, translate}))
}

// The hash reads which directives exist rather than which still owe output, so
// it is the same before and after a run generates them.
func TestTheApprovalHashIsTheSameWarmAndCold(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "gen.go")
	require.NoError(t, os.WriteFile(file, []byte("package g\n"), 0o644))
	deps := []generateDirective{{File: file, Line: 1, Command: "go run tool -out parser.go in.c"}}

	cold := depApprovalHash(deps)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parser.go"), []byte("package g\n"), 0o644))
	assert.Equal(t, cold, depApprovalHash(deps), "generating changed nothing about what exists")
}

// Every module still owing output gets its own approval, over all of that
// module's directives, and a module owing nothing asks for none.
func TestAnApprovalIsOwedPerOwingModule(t *testing.T) {
	first := under("github.com/wow/dep@v1.2.3/g/gen.go")
	second := under("github.com/wow/dep@v1.2.3/h/gen.go")
	settled := under("github.com/wow/two@v4.5.6/g/gen.go")
	deps := []generateDirective{
		{File: first, Line: 1, Command: "go run t -out a.go x.c", Label: cacheLabel(cacheRoot, first)},
		{File: settled, Line: 1, Command: "go run t -out b.go y.c", Label: cacheLabel(cacheRoot, settled)},
		{File: second, Line: 1, Command: "go run t -out c.go z.c", Label: cacheLabel(cacheRoot, second)},
	}

	owed := depApprovalsOwed(cacheRoot, deps, deps[:1])
	require.Len(t, owed, 1)
	assert.Equal(t, "github.com/wow/dep", owed[0].Path)
	assert.Equal(t, "v1.2.3", owed[0].Version)
	assert.Equal(t, depApprovalHash([]generateDirective{deps[0], deps[2]}), owed[0].Hash)
}

// Only a dependency that shipped a directive and withheld its file is owed
// anything. The dependency tree is full of stringer directives whose results
// their own repositories commit, and running those needs tools nobody installed.
func TestOnlyAMissingNamedOutputIsOwed(t *testing.T) {
	dir := t.TempDir()
	write := func(name string) { require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)) }
	write("gen.go")
	at := func(cmd string) generateDirective {
		return generateDirective{File: filepath.Join(dir, "gen.go"), Line: 1, Command: cmd}
	}

	assert.True(t, owesOutput(at("go run tool -out parser.go in.c")), "named and absent")
	assert.False(t, owesOutput(at("stringer -type=Kind")), "names no output, so nothing is known")

	read := t.TempDir()
	generated := at("go run tool -out parser.go in.c")
	generated.ReadDir = read
	assert.True(t, owesOutput(generated), "absent from the copy the go command reads too")
	require.NoError(t, os.WriteFile(filepath.Join(read, "parser.go"), []byte("x"), 0o644))
	assert.False(t, owesOutput(generated), "the go command generated it into its copy")

	write("parser.go")
	assert.False(t, owesOutput(at("go run tool -out parser.go in.c")), "the file is there")
	assert.False(t, owesOutput(at("go run tool -out=parser.go in.c")), "the joined spelling reads too")
}

// A generator the module zip left out cannot run, so it is not pending.
func TestADirectiveWithNoGeneratorInTheModuleIsDropped(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "equal_fold.go"), []byte("x"), 0o644))
	at := func(cmd string) generateDirective {
		return generateDirective{File: filepath.Join(dir, "equal_fold.go"), Line: 1, Command: cmd, Label: "dep/ascii/equal_fold.go"}
	}

	absent := at("go run equal_fold_asm.go -out equal_fold_amd64.s -stubs equal_fold_amd64.go")
	require.True(t, owesOutput(absent), "the output is genuinely missing")
	assert.Equal(t, "equal_fold_asm.go", missingGenerator(absent))
	assert.Empty(t, pendingDepDirectives([]generateDirective{absent}), "an unrunnable directive owes nothing")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "equal_fold_asm.go"), []byte("x"), 0o644))
	assert.Empty(t, missingGenerator(absent), "the generator ships after all")
	assert.Len(t, pendingDepDirectives([]generateDirective{absent}), 1, "a runnable directive stays pending")

	assert.Empty(t, missingGenerator(at("stringer -type=Kind")), "only a go run command names its sources")
	assert.Empty(t, missingGenerator(at("go run equal_fold_asm.go -stubs never_written.go")), "a name after a flag is an output, not a source")
}

// A generator binary this machine never installed cannot run, so it is dropped
// rather than failing the whole resolve for every consumer of the module.
func TestADirectiveNamingAnUninstalledToolIsDropped(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "catalog.go"), []byte("x"), 0o644))
	at := func(cmd string) generateDirective {
		return generateDirective{File: filepath.Join(dir, "catalog.go"), Line: 1, Command: cmd, Label: "x/text/catalog.go"}
	}

	absent := at("gotext-not-a-real-binary -out catalog.gen.go update")
	require.True(t, owesOutput(absent), "the output is genuinely missing")
	assert.Equal(t, "gotext-not-a-real-binary", missingTool(absent))
	assert.Empty(t, pendingDepDirectives([]generateDirective{absent}), "an uninstalled tool owes nothing")

	assert.Empty(t, missingTool(at("go run gen.go -out catalog.gen.go")), "go run carries its own program")
	assert.Empty(t, missingTool(at("sh -out catalog.gen.go")), "a tool on PATH runs")
}

// A run is grouped per module, because the clone is per repository.
func TestDirectivesGroupByTheirModule(t *testing.T) {
	a := under("github.com/wow/dep@v1/g/gen.go")
	b := under("github.com/wow/dep@v1/h/gen.go")
	other := under("github.com/wow/two@v1/g/gen.go")
	all := []generateDirective{{File: a}, {File: other}, {File: b}}

	mods := byModule(cacheRoot, all)
	require.Len(t, mods, 2)
	assert.Equal(t, "github.com/wow/dep", mods[0].Path)
	assert.Equal(t, "github.com/wow/two", mods[1].Path)
	assert.Len(t, directivesUnder(mods[0].Root, all), 2)
	assert.Len(t, directivesUnder(mods[1].Root, all), 1)
}

// The module root is the directory carrying the version, however deep the
// package sits under it.
func TestModuleRootIsTheDirectoryCarryingTheVersion(t *testing.T) {
	root := under("github.com/wow/dep@v1")
	assert.Equal(t, root, moduleRootOf(cacheRoot, root+"/a/b/c/gen.go"))
	assert.Empty(t, moduleRootOf(cacheRoot, "/home/user/checkout/a/gen.go"))
}

// The go command answers GOMODCACHE in the host's spelling and {{.Dir}} in
// another, so an NT run compared spellings of a single directory and
// grouped nothing. Everything the cache code compares is slash-spelled now.
func TestAHostSpelledFileStillFindsItsModule(t *testing.T) {
	root := under("github.com/wow/dep@v1")
	// The host's own spelling, which is what the go command hands back.
	native := filepath.FromSlash(root + "/g/gen.go")

	assert.Equal(t, root, moduleRootOf(cacheRoot, native), "the file's spelling is normalized")
	mods := byModule(cacheRoot, []generateDirective{{File: native}})
	require.Len(t, mods, 1, "and it groups rather than vanishing")
	assert.Equal(t, "github.com/wow/dep", mods[0].Path)
}

// The table blob travels with the loader that embeds it: an embed of a missing
// file does not compile, so copying the loader alone would break the package
// the copy was meant to fix.
func TestTheTableTravelsWithItsLoader(t *testing.T) {
	from := t.TempDir()
	for _, name := range []string{"parser.go", "tables.zst", "gen.go"} {
		require.NoError(t, os.WriteFile(filepath.Join(from, name), []byte(name), 0o644))
	}
	assert.ElementsMatch(t, []string{"parser.go", "tables.zst"}, generatedSiblings(from, "parser.go"))
}

// A directory the module ships no package in holds no directive worth reading.
func TestTheWalkStopsAtDirectoriesHoldingNoPackage(t *testing.T) {
	for _, name := range []string{"testdata", "vendor", "node_modules", ".git", "_ignored"} {
		assert.True(t, skipDepDir(name), name)
	}
	assert.False(t, skipDepDir("grammars"))
}

// Only a directory carrying Go source is a package directory.
func TestOnlyADirectoryWithGoSourceIsRead(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, hasGoFile(dir), "nothing in it")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644))
	assert.False(t, hasGoFile(dir), "and no Go in it")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644))
	assert.True(t, hasGoFile(dir))
}

// Nothing to satisfy asks the go command nothing.
func TestSatisfyingNothingIsAnImmediateNoOp(t *testing.T) {
	assert.NoError(t, satisfyDepDirectives(nil))
}

// go mod tidy can move a dependency's pin after the earliest pass generated for
// the older version, which leaves the new version's cache directory owing its
// output again. That gap is still the clone's to fill: the directive's input is
// a git submodule, and the cached copy carries a gitlink instead of the files.
// So the next pass must reach the clone rather than run the directive where it
// sits.
func TestASecondPassStillGeneratesInTheClone(t *testing.T) {
	cache := t.TempDir()
	pkg := filepath.Join(cache, "example.invalid", "dep@v1", "g")
	require.NoError(t, os.MkdirAll(pkg, 0o755))
	gen := filepath.Join(pkg, "gen.go")
	require.NoError(t, os.WriteFile(gen, []byte("package g\n"), 0o644))
	t.Setenv("GOMODCACHE", cache)

	err := satisfyDepDirectives([]generateDirective{{
		File:    gen,
		Line:    1,
		Command: "go run tool -out parser.go in.c",
		Label:   cacheLabel(cache, gen),
	}})

	require.Error(t, err, "the clone of a module that does not exist fails")
	assert.Contains(t, err.Error(), "example.invalid/dep", "and it names the module it cloned")
	_, statErr := os.Stat(filepath.Join(pkg, "parser.go"))
	assert.True(t, os.IsNotExist(statErr), "the directive never ran in the cache")
}

// The cache is read only by design, and a directive writes beside the file that
// declares it. The mode goes back, so a later read sees what it expects.
func TestWritableDirRestoresTheMode(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o555))
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	require.NoError(t, withWritableDir(dir, func() error {
		return os.WriteFile(filepath.Join(dir, "written"), []byte("x"), 0o644)
	}))

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o555), info.Mode().Perm(), "the mode is put back")
	_, err = os.Stat(filepath.Join(dir, "written"))
	assert.NoError(t, err, "and the write landed")
}

// filepath.ToSlash reads the COMPILE TARGET, and the published binary is a cosmo
// build whose separator is already a slash. The host decides here.
func TestASlashSpellingReadsTheHostNotTheTarget(t *testing.T) {
	nt := `C:\Users\runneradmin\go\pkg\mod`
	assert.Equal(t, "C:/Users/runneradmin/go/pkg/mod", slashPathFor("windows", nt))
	assert.Equal(t, "/home/u/go/pkg/mod", slashPathFor("linux", "/home/u/go/pkg/mod"))
	assert.Equal(t, nt, slashPathFor("linux", nt), "a backslash is a legal posix name")
}
