package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cacheRoot = "/root/go/pkg/mod"

// A cached directory names the module and the version together. The clone needs
// them apart: a single is the repository to fetch, the other asks the go
// command for the commit.
func TestSplitVersionSeparatesTheModuleFromItsVersion(t *testing.T) {
	path, version := splitVersion(cacheRoot + "/github.com/wow/slopfix@v0.0.0-20260912-abc")
	assert.Equal(t, cacheRoot+"/github.com/wow/slopfix", path)
	assert.Equal(t, "v0.0.0-20260912-abc", version)

	path, version = splitVersion("/home/user/checkout")
	assert.Empty(t, path)
	assert.Empty(t, version)
}

// The approval hash reads the label, and the label carries no version: a
// dependency bump that changed no directive must not demand a fresh approval.
func TestCacheLabelDropsTheVersion(t *testing.T) {
	got := cacheLabel(cacheRoot, cacheRoot+"/github.com/wow/slopfix@v1.2.3/grammars/bash/gen.go")
	assert.Equal(t, "github.com/wow/slopfix/grammars/bash/gen.go", got)
}

// Versions of the same dependency hash alike while their directives agree, and
// differently as soon as a single changes.
func TestTheApprovalHashIgnoresAVersionBump(t *testing.T) {
	at := func(v string) []generateDirective {
		file := cacheRoot + "/github.com/wow/dep@" + v + "/g/gen.go"
		return []generateDirective{{
			File:    file,
			Line:    3,
			Command: "go run tool -out parser.go in.c",
			Label:   cacheLabel(cacheRoot, file),
		}}
	}
	own := []generateDirective{{File: "src/a.go", Line: 1, Command: "go run own"}}

	assert.Equal(t, approvalHash(own, at("v1.0.0")), approvalHash(own, at("v2.0.0")))

	changed := at("v2.0.0")
	changed[0].Command = "go run tool -out parser.go other.c"
	assert.NotEqual(t, approvalHash(own, at("v1.0.0")), approvalHash(own, changed))
}

// The hash reads which directives exist rather than which still owe output, so
// it is the same before and after a run generates them.
func TestTheApprovalHashIsTheSameWarmAndCold(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "gen.go")
	require.NoError(t, os.WriteFile(file, []byte("package g\n"), 0o644))
	deps := []generateDirective{{File: file, Line: 1, Command: "go run tool -out parser.go in.c"}}

	cold := approvalHash(nil, deps)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "parser.go"), []byte("package g\n"), 0o644))
	assert.Equal(t, cold, approvalHash(nil, deps), "generating changed nothing about what exists")
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

	write("parser.go")
	assert.False(t, owesOutput(at("go run tool -out parser.go in.c")), "the file is there")
	assert.False(t, owesOutput(at("go run tool -out=parser.go in.c")), "the joined spelling reads too")
}

// A run is grouped per module, because the clone is per repository.
func TestDirectivesGroupByTheirModule(t *testing.T) {
	a := cacheRoot + "/github.com/wow/dep@v1/g/gen.go"
	b := cacheRoot + "/github.com/wow/dep@v1/h/gen.go"
	other := cacheRoot + "/github.com/wow/two@v1/g/gen.go"
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
	root := cacheRoot + "/github.com/wow/dep@v1"
	assert.Equal(t, root, moduleRootOf(cacheRoot, root+"/a/b/c/gen.go"))
	assert.Empty(t, moduleRootOf(cacheRoot, "/home/user/checkout/a/gen.go"))
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

// Nothing to satisfy asks the go command nothing.
func TestSatisfyingNothingIsAnImmediateNoOp(t *testing.T) {
	assert.NoError(t, satisfyDepDirectives(nil))
}

// go mod tidy can move a dependency's pin after the first pass generated for
// the older version, which leaves the new version's cache directory owing its
// output again. That gap is still the clone's to fill: the directive's input is
// a git submodule, and the cached copy carries a gitlink instead of the files.
// So the second pass must reach the clone rather than run the directive where it
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
