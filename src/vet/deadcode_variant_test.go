package vet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A helper only a test calls is not dead: the package's test variant uses it.
// Reading the plain variant instead made every test-only helper in this repo a
// vet violation, and reported a genuinely dead symbol repeatedly.
func TestDeadCodeAnswersFromTheVariantThatHoldsTheTests(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	write("go.mod", "module testmod\n\ngo 1.21\n")
	write("lib.go", `package lib

func usedByTestOnly() int { return 7 }

func trulyDead() int { return 8 }
`)
	write("lib_test.go", `package lib

import "testing"

func TestUsed(t *testing.T) {
	t.Serial()
	if usedByTestOnly() != 7 {
		t.Fatal("bad")
	}
}
`)

	oldWd, _ := os.Getwd()
	require.NoError(t, os.Chdir(dir))
	defer os.Chdir(oldWd)

	_, err := RunOnPattern("./...", false, nil)

	require.Error(t, err, "trulyDead is unreferenced everywhere, so the run must fail")
	msg := err.Error()
	assert.NotContains(t, msg, "usedByTestOnly")
	assert.Equal(t, 1, strings.Count(msg, "trulyDead"),
		"one package, one report -- not one per loaded variant")
}
