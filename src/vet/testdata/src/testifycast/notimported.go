package testifycast

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// CaseNotImportedPackage compares an untyped literal against a value whose
// type's package (testifycast/modes) is NOT imported by this file — getMode is
// declared in fixture.go, which imports it. The conversion is spelled
// modes.Mode(...), so the edit must record the testifycast/modes import for
// addition; without it the rewritten file would fail to load (the io/fs
// alias-origin shape: os.FileMode operands yield fs.FileMode conversions in
// files that only import os).
func CaseNotImportedPackage(t *testing.T) {
	assert.NotEqual(t, 0, getMode())
}
