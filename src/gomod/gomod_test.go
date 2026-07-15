package gomod

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile writes content to dir/name, creating dir as needed.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// chdir changes into dir for the duration of the test.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { _ = os.Chdir(orig) })
}

// newModule creates a temporary module rooted at a temp dir and chdirs into it.
func newModule(t *testing.T, modPath string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module "+modPath+"\n\ngo 1.25\n")
	chdir(t, root)
	return root
}

func TestHasMainPackage_IgnoresBuildIgnoreMain(t *testing.T) {
	root := t.TempDir()
	// A directory whose ONLY package main file is //go:build ignore must not
	// be reported as a main package.
	writeFile(t, root, "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	assert.False(t, hasMainPackage(root),
		"//go:build ignore package main must not count as a main package")
}

func TestHasMainPackage_IgnoresPlusBuildIgnoreMain(t *testing.T) {
	root := t.TempDir()
	// Old-style "// +build ignore" must also be honored.
	writeFile(t, root, "gen.go", "// +build ignore\n\npackage main\n\nfunc main() {}\n")
	assert.False(t, hasMainPackage(root),
		"// +build ignore package main must not count as a main package")
}

func TestHasMainPackage_NormalMainIsFound(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	assert.True(t, hasMainPackage(root), "a normal package main dir must be found")
}

func TestHasMainPackage_RealMainAlongsideIgnoredGenerator(t *testing.T) {
	root := t.TempDir()
	// A real package main next to an //go:build ignore package main generator:
	// the real main must still be discovered (cmd/... discovery unaffected).
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, root, "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	assert.True(t, hasMainPackage(root),
		"a real package main must still be found next to an ignored generator")
}

func TestHasMainPackage_BenchDirWithOnlyIgnoredGeneratorIsNotMain(t *testing.T) {
	root := t.TempDir()
	// A package bench dir whose only package main file is an ignored generator
	// must NOT be treated as a main package (the go-regex-compiler bug).
	writeFile(t, root, "bench_test.go", "package bench\n")
	writeFile(t, root, "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	assert.False(t, hasMainPackage(root),
		"a package bench dir with only an ignored main generator must not be a main package")
}

func TestFindMainPackages_HonorsBuildConstraints(t *testing.T) {
	modPath := "example.com/honors"
	newModule(t, modPath)

	// Real main package.
	writeFile(t, "cmd/tool", "main.go", "package main\n\nfunc main() {}\n")
	// bench/ with an ignored generator + a buildable package bench.
	writeFile(t, "bench", "bench_test.go", "package bench\n")
	writeFile(t, "bench", "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	// e2e/ with an ignored generator + a buildable package e2e.
	writeFile(t, "e2e", "e2e_test.go", "package e2e\n")
	writeFile(t, "e2e", "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")

	pkgs, err := FindMainPackages()
	require.NoError(t, err)
	sort.Strings(pkgs)

	assert.Equal(t, []string{modPath + "/cmd/tool"}, pkgs,
		"only the real main package must be discovered; ignored generators in bench/e2e must be excluded")
}

func TestHasMainPackage_OnlyConstraintChecksMainCandidates(t *testing.T) {
	root := t.TempDir()
	// A directory full of non-main files plus a single real package main.
	writeFile(t, root, "a.go", "package lib\n")
	writeFile(t, root, "b.go", "package lib\n")
	writeFile(t, root, "c.go", "package lib\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	// A non-test file that is NOT package main must never reach the
	// (expensive) build-constraint check; only the package-main file may.
	var checked []string
	orig := matchFile
	matchFile = func(dir, name string) (bool, error) {
		checked = append(checked, name)
		return orig(dir, name)
	}
	t.Cleanup(func() { matchFile = orig })

	assert.True(t, hasMainPackage(root))
	assert.Equal(t, []string{"main.go"}, checked,
		"the build-constraint check must run only on package-main candidates, not every .go file")
}

func TestFindMainPackages_RootMain(t *testing.T) {
	modPath := "example.com/rootmain"
	newModule(t, modPath)
	writeFile(t, ".", "main.go", "package main\n\nfunc main() {}\n")

	pkgs, err := FindMainPackages()
	require.NoError(t, err)
	assert.Equal(t, []string{modPath}, pkgs)
}

// k8sHeader is a multi-line /* */ license header in the style the Kubernetes
// project stamps on every file. The old hand-rolled package-clause scanner
// skipped only the FIRST line of such a comment ("/*"): the continuation
// lines matched none of its prefixes, so it bailed before reaching the
// package clause and the main package silently vanished from the build.
const k8sHeader = `/*
Copyright 2024 The Example Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

`

func TestPackageNameFromFile_BlockCommentHeader(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "main.go", k8sHeader+"package main\n\nfunc main() {}\n")
	assert.Equal(t, "main", packageNameFromFile(filepath.Join(root, "main.go")),
		"a multi-line /* */ license header must not hide the package clause")
}

func TestPackageNameFromFile_Forms(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		name, content, want string
	}{
		{"trailing_line_comment.go", "package main // entry point\n", "main"},
		{"inline_block_comment.go", "package main /* entry */\n\nfunc main() {}\n", "main"},
		{"line_comments.go", "// a\n// b\npackage lib\n", "lib"},
		{"build_constraint.go", "//go:build linux\n\npackage lib\n", "lib"},
		{"single_line_block.go", "/* one-liner */\npackage lib\n", "lib"},
		{"not_go_source.go", "this is not go\n", ""},
		{"empty.go", "", ""},
	}
	for _, c := range cases {
		writeFile(t, root, c.name, c.content)
		assert.Equal(t, c.want, packageNameFromFile(filepath.Join(root, c.name)), c.name)
	}
	assert.Equal(t, "", packageNameFromFile(filepath.Join(root, "does-not-exist.go")))
}

func TestHasMainPackage_BlockCommentHeaderMain(t *testing.T) {
	root := t.TempDir()
	// The end-to-end shape of the bug: a main package whose ONLY file starts
	// with a k8s-style block-comment header was silently not built (missed by
	// ResolveBuildTargets and the memlimit guard injection).
	writeFile(t, root, "main.go", k8sHeader+"package main\n\nfunc main() {}\n")
	assert.True(t, hasMainPackage(root),
		"a main package behind a block-comment license header must be discovered")
}

func TestIsNestedModule(t *testing.T) {
	newModule(t, "example.com/outer")
	writeFile(t, "plain", "lib.go", "package lib\n")
	writeFile(t, "nested", "go.mod", "module example.com/nested\n\ngo 1.25\n")

	assert.False(t, IsNestedModule("."), "the walk root is never a NESTED module")
	assert.False(t, IsNestedModule("plain"))
	assert.True(t, IsNestedModule("nested"))
	assert.False(t, IsNestedModule("does-not-exist"))
}

func TestFindMainPackages_SkipsNestedModule(t *testing.T) {
	modPath := "example.com/outer"
	newModule(t, modPath)
	writeFile(t, "cmd/app", "main.go", "package main\n\nfunc main() {}\n")
	// A nested module (e.g. a vendored compat copy like src/compat/go-isatty)
	// may contain main packages of its own; they belong to that module and
	// must not be reported as buildable import paths of the outer one.
	writeFile(t, "compat/tool", "go.mod", "module example.com/tool\n\ngo 1.25\n")
	writeFile(t, "compat/tool", "main.go", "package main\n\nfunc main() {}\n")

	pkgs, err := FindMainPackages()
	require.NoError(t, err)
	assert.Equal(t, []string{modPath + "/cmd/app"}, pkgs)
}

func TestFindMainPackagesForTarget(t *testing.T) {
	newModule(t, "example.com/multi")
	writeFile(t, "cmd/everywhere", "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, "cmd/wasmonly", "main.go", "//go:build js && wasm\n\npackage main\n\nfunc main() {}\n")
	writeFile(t, "cmd/linuxonly", "main.go", "//go:build linux\n\npackage main\n\nfunc main() {}\n")
	// The generator idiom stays excluded in EVERY context.
	writeFile(t, "lib", "lib.go", "package lib\n")
	writeFile(t, "lib", "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")

	js, err := FindMainPackagesForTarget("js", "wasm")
	require.NoError(t, err)
	sort.Strings(js)
	assert.Equal(t, []string{
		"example.com/multi/cmd/everywhere",
		"example.com/multi/cmd/wasmonly",
	}, js, "js/wasm context must see the js&&wasm-guarded main, not the linux one")

	linux, err := FindMainPackagesForTarget("linux", "amd64")
	require.NoError(t, err)
	sort.Strings(linux)
	assert.Equal(t, []string{
		"example.com/multi/cmd/everywhere",
		"example.com/multi/cmd/linuxonly",
	}, linux, "linux context must see the linux-guarded main regardless of host")

	darwin, err := FindMainPackagesForTarget("darwin", "arm64")
	require.NoError(t, err)
	assert.Equal(t, []string{"example.com/multi/cmd/everywhere"}, darwin,
		"an unmatched context must only see the unconstrained main")
}

func TestFindMainPackagesSkipsMemLimitGuard(t *testing.T) {
	newModule(t, "example.com/guarded")
	// A host-only main dir carrying an injected memlimit guard (the guard is
	// an UNCONSTRAINED package main file): the guard must not leak this dir
	// into other targets' discovery.
	writeFile(t, "cmd/linuxonly", "main.go", "//go:build linux\n\npackage main\n\nfunc main() {}\n")
	writeFile(t, "cmd/linuxonly", MemLimitGuardFileName, "package main\n")
	// A dir whose ONLY main-ish file is a stale guard is not a main package
	// in any context.
	writeFile(t, "cmd/stale", MemLimitGuardFileName, "package main\n")

	js, err := FindMainPackagesForTarget("js", "wasm")
	require.NoError(t, err)
	assert.Empty(t, js, "the unconstrained guard must not make a linux-only main dir visible to js/wasm")

	linux, err := FindMainPackagesForTarget("linux", "amd64")
	require.NoError(t, err)
	assert.Equal(t, []string{"example.com/guarded/cmd/linuxonly"}, linux,
		"the real main is still discovered under its own context; the guard-only dir is not")

	host, err := FindMainPackages()
	require.NoError(t, err)
	assert.NotContains(t, host, "example.com/guarded/cmd/stale",
		"a stale guard alone must not make a dir a main package under the host context either")
}
