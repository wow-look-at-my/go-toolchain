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

func TestFindMainPackages_RootMain(t *testing.T) {
	modPath := "example.com/rootmain"
	newModule(t, modPath)
	writeFile(t, ".", "main.go", "package main\n\nfunc main() {}\n")

	pkgs, err := FindMainPackages()
	require.NoError(t, err)
	assert.Equal(t, []string{modPath}, pkgs)
}
