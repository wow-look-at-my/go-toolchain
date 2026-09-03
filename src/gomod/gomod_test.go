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

func TestReadModulePath(t *testing.T) {
	dir := t.TempDir()
	assert.Equal(t, "", ReadModulePath(dir), "a directory with no go.mod names no module")

	writeFile(t, dir, "go.mod", "module github.com/user/pkg\n\ngo 1.21\n")
	assert.Equal(t, "github.com/user/pkg", ReadModulePath(dir))
}

func TestReadModulePathEmptyFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "")
	assert.Equal(t, "", ReadModulePath(dir))
}

func TestReadModulePathExtraWhitespace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module   github.com/user/pkg  \n")
	assert.Equal(t, "github.com/user/pkg", ReadModulePath(dir))
}

// newModule writes a go.mod in a temp dir and returns the root to walk.
func newModule(t *testing.T, modPath string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module "+modPath+"\n\ngo 1.25\n")
	return root
}

func TestHasMainPackage_IgnoresBuildIgnoreMain(t *testing.T) {
	root := t.TempDir()
	// A dir whose only package main file is //go:build ignore is not a main package.
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
	// A real main next to an ignored generator main must still be discovered.
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, root, "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	assert.True(t, hasMainPackage(root),
		"a real package main must still be found next to an ignored generator")
}

func TestHasMainPackage_BenchDirWithOnlyIgnoredGeneratorIsNotMain(t *testing.T) {
	root := t.TempDir()
	// A package bench dir with only an ignored generator main is not a main package.
	writeFile(t, root, "bench_test.go", "package bench\n")
	writeFile(t, root, "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	assert.False(t, hasMainPackage(root),
		"a package bench dir with only an ignored main generator must not be a main package")
}

func TestFindMainPackages_HonorsBuildConstraints(t *testing.T) {
	modPath := "example.com/honors"
	root := newModule(t, modPath)

	// Real main package.
	writeFile(t, filepath.Join(root, "cmd", "tool"), "main.go", "package main\n\nfunc main() {}\n")
	// bench/ with an ignored generator + a buildable package bench.
	writeFile(t, filepath.Join(root, "bench"), "bench_test.go", "package bench\n")
	writeFile(t, filepath.Join(root, "bench"), "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")
	// e2e/ with an ignored generator + a buildable package e2e.
	writeFile(t, filepath.Join(root, "e2e"), "e2e_test.go", "package e2e\n")
	writeFile(t, filepath.Join(root, "e2e"), "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")

	pkgs, err := FindMainPackages(root)
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
	// A non-main file must never reach the build-constraint check.
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
	root := newModule(t, modPath)
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")

	pkgs, err := FindMainPackages(root)
	require.NoError(t, err)
	assert.Equal(t, []string{modPath}, pkgs)
}

// k8sHeader is a multi-line /* */ license header, the style Kubernetes uses.
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
	// End-to-end: a main file behind a block-comment header must still be found.
	writeFile(t, root, "main.go", k8sHeader+"package main\n\nfunc main() {}\n")
	assert.True(t, hasMainPackage(root),
		"a main package behind a block-comment license header must be discovered")
}

func TestIsNestedModule(t *testing.T) {
	root := newModule(t, "example.com/outer")
	writeFile(t, filepath.Join(root, "plain"), "lib.go", "package lib\n")
	writeFile(t, filepath.Join(root, "nested"), "go.mod", "module example.com/nested\n\ngo 1.25\n")

	assert.False(t, IsNestedModule("."), "the walk root is never a NESTED module")
	assert.False(t, IsNestedModule(filepath.Join(root, "plain")))
	assert.True(t, IsNestedModule(filepath.Join(root, "nested")))
	assert.False(t, IsNestedModule(filepath.Join(root, "does-not-exist")))
}

func TestFindMainPackages_SkipsNestedModule(t *testing.T) {
	modPath := "example.com/outer"
	root := newModule(t, modPath)
	writeFile(t, filepath.Join(root, "cmd", "app"), "main.go", "package main\n\nfunc main() {}\n")
	// A nested module's own main packages belong to it, not the outer module.
	writeFile(t, filepath.Join(root, "compat", "tool"), "go.mod", "module example.com/tool\n\ngo 1.25\n")
	writeFile(t, filepath.Join(root, "compat", "tool"), "main.go", "package main\n\nfunc main() {}\n")

	pkgs, err := FindMainPackages(root)
	require.NoError(t, err)
	assert.Equal(t, []string{modPath + "/cmd/app"}, pkgs)
}

func TestFindMainPackagesForTarget(t *testing.T) {
	root := newModule(t, "example.com/multi")
	writeFile(t, filepath.Join(root, "cmd", "everywhere"), "main.go", "package main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "cmd", "wasmonly"), "main.go", "//go:build js && wasm\n\npackage main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "cmd", "linuxonly"), "main.go", "//go:build linux\n\npackage main\n\nfunc main() {}\n")
	// The generator idiom stays excluded in EVERY context.
	writeFile(t, filepath.Join(root, "lib"), "lib.go", "package lib\n")
	writeFile(t, filepath.Join(root, "lib"), "gen.go", "//go:build ignore\n\npackage main\n\nfunc main() {}\n")

	js, err := FindMainPackagesForTarget(root, "js", "wasm")
	require.NoError(t, err)
	sort.Strings(js)
	assert.Equal(t, []string{
		"example.com/multi/cmd/everywhere",
		"example.com/multi/cmd/wasmonly",
	}, js, "js/wasm context must see the js&&wasm-guarded main, not the linux one")

	linux, err := FindMainPackagesForTarget(root, "linux", "amd64")
	require.NoError(t, err)
	sort.Strings(linux)
	assert.Equal(t, []string{
		"example.com/multi/cmd/everywhere",
		"example.com/multi/cmd/linuxonly",
	}, linux, "linux context must see the linux-guarded main regardless of host")

	darwin, err := FindMainPackagesForTarget(root, "darwin", "arm64")
	require.NoError(t, err)
	assert.Equal(t, []string{"example.com/multi/cmd/everywhere"}, darwin,
		"an unmatched context must only see the unconstrained main")
}

func TestFindMainPackagesSkipsMemLimitGuard(t *testing.T) {
	root := newModule(t, "example.com/guarded")
	// The unconstrained guard file must not leak this dir into other targets.
	writeFile(t, filepath.Join(root, "cmd", "linuxonly"), "main.go", "//go:build linux\n\npackage main\n\nfunc main() {}\n")
	writeFile(t, filepath.Join(root, "cmd", "linuxonly"), MemLimitGuardFileName, "package main\n")
	// A dir whose only main-ish file is a stale guard is not a main package.
	writeFile(t, filepath.Join(root, "cmd", "stale"), MemLimitGuardFileName, "package main\n")

	js, err := FindMainPackagesForTarget(root, "js", "wasm")
	require.NoError(t, err)
	assert.Empty(t, js, "the unconstrained guard must not make a linux-only main dir visible to js/wasm")

	linux, err := FindMainPackagesForTarget(root, "linux", "amd64")
	require.NoError(t, err)
	assert.Equal(t, []string{"example.com/guarded/cmd/linuxonly"}, linux,
		"the real main is still discovered under its own context; the guard-only dir is not")

	host, err := FindMainPackages(root)
	require.NoError(t, err)
	assert.NotContains(t, host, "example.com/guarded/cmd/stale",
		"a stale guard alone must not make a dir a main package under the host context either")
}
