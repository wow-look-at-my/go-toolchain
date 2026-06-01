// Package testifycast contains input fixtures for the testifycast analyzer.
// The analyzer rewrites cross-type Equal/NotEqual operands so they pass against
// upstream testify; the test applies the analyzer and checks the result.
package testifycast

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Functions returning concrete non-constant values: the realistic shape is
// comparing a literal against the result of a call, not two literals.
func getFloat64() float64 { return 0 }
func getInt() int         { return 0 }
func getInt64() int64     { return 0 }
func getInt32() int32     { return 0 }
func getUint() uint       { return 0 }

// Celsius is a numeric named type (same kind as float64).
type Celsius float64

func getCelsius() Celsius { return 0 }

// Name is a string-kinded named type (non-numeric, same kind as string).
type Name string

func getName() Name { return "" }

// notTestify has its own Equal method that must never be rewritten.
type notTestify struct{}

func (notTestify) Equal(expected, actual interface{}) bool { return true }

// Case 1: untyped int literal vs float64 call result -> wrap the literal.
func CaseEqualLiteralVsFloat(t *testing.T) {
	assert.Equal(t, 0, getFloat64())
}

// Case 2: operands swapped -> wrap the literal that sits in the actual slot.
func CaseEqualFloatVsLiteral(t *testing.T) {
	assert.Equal(t, getFloat64(), 0)
}

// Case 3: Equalf -> cast inserted, format string and args untouched.
func CaseEqualfFormatArgs(t *testing.T) {
	k := 3
	require.Equalf(t, 0, getFloat64(), "x=%d", k)
}

// Case 4: method form on *assert.Assertions (no leading t).
func CaseMethodForm(t *testing.T) {
	a := assert.New(t)
	a.Equal(0, getFloat64())
}

// Case 5: typed int32 vs int64 mismatch -> wrap the expected in int64(...).
func CaseTypedIntWidths(t *testing.T) {
	assert.Equal(t, getInt32(), getInt64())
}

// uint example from the task: wrap the actual literal in uint(...).
func CaseUintActualLiteral(t *testing.T) {
	require.Equal(t, getUint(), 10)
}

// NotEqual is handled identically to Equal.
func CaseNotEqual(t *testing.T) {
	assert.NotEqual(t, 0, getFloat64())
}

// Case 8 (numeric named type): Celsius vs float64 -> conversion to float64.
func CaseNamedNumeric(t *testing.T) {
	assert.Equal(t, getCelsius(), getFloat64())
}

// Rule 5 (non-numeric same-kind named type): Name vs string -> conversion.
func CaseNamedString(t *testing.T) {
	assert.Equal(t, getName(), "")
}

// --- Negative cases: the analyzer must NOT change these ---

// Case 6: identical static types -> no change.
func CaseIdenticalTypes(t *testing.T) {
	assert.Equal(t, getInt(), getInt())
}

// Case 7: non-numeric mismatch (string vs []byte) -> no change.
func CaseStringVsBytes(t *testing.T) {
	assert.Equal(t, "x", []byte("x"))
}

// Case 10: fractional constant vs integer -> no change (would truncate).
func CaseFractionalTruncation(t *testing.T) {
	assert.Equal(t, 1.5, getInt())
}

// Case 11: a non-testify Equal method -> no change.
func CaseNotTestify(t *testing.T) {
	var x notTestify
	x.Equal(0, getFloat64())
}

// EqualValues already does convertible comparison upstream -> no change.
func CaseEqualValues(t *testing.T) {
	assert.EqualValues(t, 0, getFloat64())
}

// --- Idempotency: already-converted operands must NOT be re-wrapped ---

func CaseAlreadyCastLiteral(t *testing.T) {
	assert.Equal(t, float64(0), getFloat64())
}

func CaseAlreadyCastTyped(t *testing.T) {
	assert.Equal(t, int64(getInt32()), getInt64())
}
