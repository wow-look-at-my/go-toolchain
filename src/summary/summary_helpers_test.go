package summary

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShortPkg(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"github.com/user/pkg/subpkg", "subpkg"},
		{"github.com/user/pkg", "pkg"},
		{"main", "main"},
		{"", ""},
		{"a/b/c/d", "d"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, shortPkg(tc.input))
	}
}

func TestStripCPUSuffix(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"BenchmarkFoo-8", "BenchmarkFoo"},
		{"BenchmarkFoo-128", "BenchmarkFoo"},
		{"BenchmarkFoo", "BenchmarkFoo"},
		{"Bench-mark-8", "Bench-mark"},
		{"", ""},
		{"-", "-"}, // the dash sits at the head, so nothing is stripped
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, stripCPUSuffix(tc.input))
	}
}

func TestFormatBenchTime(t *testing.T) {
	tests := []struct {
		ns   float64
		want string
	}{
		{0, "0.0 ns"},
		{100, "100.0 ns"},
		{999, "999.0 ns"},
		{1500, "1.50 us"},
		{1500000, "1.50 ms"},
		{1500000000, "1.50 s"},
		{1e9, "1.00 s"},
		{1e6, "1.00 ms"},
		{1e3, "1.00 us"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatBenchTime(tc.ns), "ns=%v", tc.ns)
	}
}

func TestFormatBenchBytes(t *testing.T) {
	tests := []struct {
		b    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{2621440, "2.5 MB"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, formatBenchBytes(tc.b), "b=%v", tc.b)
	}
}

func TestReadModulePath(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	// No go.mod
	assert.Equal(t, "", readModulePath())

	// Valid go.mod
	os.WriteFile("go.mod", []byte("module github.com/user/pkg\n\ngo 1.21\n"), 0644)
	assert.Equal(t, "github.com/user/pkg", readModulePath())
}

func TestReadModulePathEmptyFile(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile("go.mod", []byte(""), 0644)
	assert.Equal(t, "", readModulePath())
}

func TestReadModulePathExtraWhitespace(t *testing.T) {
	dir := t.TempDir()
	origDir, _ := os.Getwd()
	os.Chdir(dir)
	defer os.Chdir(origDir)

	os.WriteFile("go.mod", []byte("module   github.com/user/pkg  \n"), 0644)
	assert.Equal(t, "github.com/user/pkg", readModulePath())
}
