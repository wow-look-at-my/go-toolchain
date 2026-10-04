package vet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnifiedDiffRendersHeaderAndHunks(t *testing.T) {
	t.Serial()
	old := "package p\n\nfunc f(){println(1)}\n"
	want := "package p\n\nfunc f() {\n\tprintln(1)\n}\n"

	got, err := unifiedDiff("f.go", []byte(old), []byte(want))
	require.NoError(t, err)

	assert.Contains(t, got, "--- f.go\tcurrent\n")
	assert.Contains(t, got, "+++ f.go\tcanonical\n")
	assert.Contains(t, got, "-func f(){println(1)}\n")
	assert.Contains(t, got, "+func f() {\n")
	assert.Contains(t, got, "+\tprintln(1)\n")
	assert.Contains(t, got, "+}\n")
	// Unchanged context lines carry no +/- marker.
	assert.Contains(t, got, " package p\n")
}

func TestUnifiedDiffIdenticalInputsProduceNoHunks(t *testing.T) {
	t.Serial()
	same := "package p\n"
	got, err := unifiedDiff("f.go", []byte(same), []byte(same))
	require.NoError(t, err)
	assert.Empty(t, got)
}
