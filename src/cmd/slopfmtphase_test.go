package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// write puts a file at path under dir, creating the directories above it.
func write(t *testing.T, dir, path, body string) {
	t.Helper()
	full := filepath.Join(dir, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
}

// TestTheScanReadsEveryLanguageTheExtractorKnows is the reason the rule left
// vet: a shell script and a workflow carry the same stale prose a Go comment
// does, and no Go analyzer ever looked at either.
func TestTheScanReadsEveryLanguageTheExtractorKnows(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "a.go", "package p\n\n// the walk has 3 phases\n")
	write(t, dir, "run.sh", "#!/bin/sh\n# the sweep runs twice\n")
	write(t, dir, "ci.yml", "# holds 4 jobs\njobs: {}\n")

	found := map[string]string{}
	for _, path := range slopfmtFiles(dir) {
		src, err := os.ReadFile(path)
		require.NoError(t, err)
		for _, hit := range commentNumberFindings(path, string(src)) {
			found[filepath.Base(path)] = hit.Number
		}
	}
	assert.Equal(t, map[string]string{"a.go": "3", "run.sh": "twice", "ci.yml": "4"}, found)
}

// TestAFindingNamesItsLineAndColumn pins what a warning points at. A report
// that names the file alone leaves the reader searching it.
func TestAFindingNamesItsLineAndColumn(t *testing.T) {
	hits := commentNumberFindings("x.go", "package p\n\n// fine\n// holds 5 entries\n")
	require.Len(t, hits, 1)
	assert.Equal(t, 4, hits[0].Line)
	assert.Equal(t, 10, hits[0].Col)
}

// TestASentenceNamingSeveralNumbersCostsOneWarning pins the budget's unit: the
// repair is a rewrite of the line, whatever it counts.
func TestASentenceNamingSeveralNumbersCostsOneWarning(t *testing.T) {
	hits := commentNumberFindings("x.go", "package p\n\n// holds 5 entries across 3 shards\n")
	assert.Len(t, hits, 1)
}

// TestTheScanSkipsTextItsAuthorDoesNotOwn pins the exclusions. A nested module
// keeps its own prose, and a build output is written rather than authored.
func TestTheScanSkipsTextItsAuthorDoesNotOwn(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "keep.go", "package p\n")
	write(t, dir, "inner/go.mod", "module y\n")
	write(t, dir, "inner/skip.go", "package q\n")
	write(t, dir, "vendor/dep/skip.go", "package r\n")
	write(t, dir, ".git/hooks/skip.sh", "# hook\n")
	write(t, dir, filepath.Join(outputDir, "skip.go"), "package s\n")

	var names []string
	for _, path := range slopfmtFiles(dir) {
		names = append(names, filepath.Base(path))
	}
	assert.Equal(t, []string{"keep.go"}, names)
}

// TestATreeWithNoModuleAtItsRootIsStillScanned pins the case the nested-module
// skip would otherwise empty: a repo whose modules all sit below the root.
func TestATreeWithNoModuleAtItsRootIsStillScanned(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "svc/go.mod", "module x\n")
	write(t, dir, "svc/a.go", "package p\n\n// holds 3 entries\n")

	var names []string
	for _, path := range slopfmtFiles(dir) {
		names = append(names, filepath.Base(path))
	}
	assert.Contains(t, names, "a.go")
}

// TestAFileTooLargeToBeProseIsSkipped pins the bound. A committed blob costs
// more to read than the prose it holds.
func TestAFileTooLargeToBeProseIsSkipped(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n")
	write(t, dir, "big.go", "package p\n// "+string(make([]byte, slopfmtMaxFileBytes))+"\n")

	assert.Empty(t, slopfmtFiles(dir))
}

// A digit run against a letter is a name, and COMMENT-SCAN.md says so: sha256,
// amd64 and p95 stay. The hardware words a driver or an analyzer is written in
// take the same shape, and a stale dependency reported every one of them.
func TestADigitAgainstALetterIsANameAndStays(t *testing.T) {
	src := "// chapter-12 tables, 64-bit ops, a 32-bit multiply and amd64\npackage p\n"
	assert.Empty(t, commentNumberFindings("p.go", src))
}

// The exemption is narrow. A digit standing alone is still a count.
func TestADigitStandingAloneIsStillACount(t *testing.T) {
	src := "// The tables run to 12 sections.\npackage p\n"
	assert.NotEmpty(t, commentNumberFindings("p.go", src))
}
