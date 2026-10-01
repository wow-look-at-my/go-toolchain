// Package testifycast contains input fixtures for the testifycast analyzer.
// The analyzer rewrites cross-type Equal/NotEqual operands so they pass against
// upstream testify; the test applies the analyzer and checks the result.
package testifycast

import (
	"testing"
	"time"

	"testifycast/modes"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Functions returning concrete non-constant values: the realistic shape is
// comparing a literal against the result of a call, not two literals.
func getFloat64() float64 { return 0 }
func getInt() int         { return 0 }
func getInt64() int64     { return 0 }
func getInt32() int32     { return 0 }
func getInt16() int16     { return 0 }
func getUint() uint       { return 0 }

// getDuration returns a named numeric type defined in another package, so the
// conversion must be spelled with the file's import qualifier (time.Duration).
func getDuration() time.Duration { return 0 }

// getMode returns a named numeric type from a package that notimported.go
// (which asserts on it) does NOT import — the conversion inserted there must
// also add the modes import.
func getMode() modes.Mode { return 0 }

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

// Non-numeric same-kind named type with the *literal* on the expected side:
// the string constant must still be wrapped (the numeric representability
// guard must not block a non-numeric conversion).
func CaseNamedStringLiteralExpected(t *testing.T) {
	assert.Equal(t, "", getName())
}

// Cross-package named numeric type: conversion spelled with the import
// qualifier (time.Duration), exercising the alias-resolution path.
func CaseCrossPackageType(t *testing.T) {
	assert.Equal(t, 0, getDuration())
}

// --- Ordering assertions (Greater/Less family): upstream compareTwoValues
// fails cross-kind operands with "Elements should be the same type", so they
// need the same conversions as Equal. These mirror real misses in
// go-font-renderer (ttf.TestParseHhea, hinter.TestSuperRoundNegative). ---

// int16 field vs untyped 0 -> wrap the literal in int16(...).
func CaseGreaterInt16VsLiteral(t *testing.T) {
	assert.Greater(t, getInt16(), 0)
}

// float64 result vs untyped 0 -> wrap the literal in float64(...).
func CaseLessFloatVsLiteral(t *testing.T) {
	assert.Less(t, getFloat64(), 0)
}

// GreaterOrEqual with typed width mismatch -> wrap e1 in int64(...).
func CaseGreaterOrEqualWidths(t *testing.T) {
	assert.GreaterOrEqual(t, getInt32(), getInt64())
}

// require package form.
func CaseRequireLess(t *testing.T) {
	require.Less(t, getInt16(), 100)
}

// f-variant: format string and args untouched.
func CaseLessfFormatArgs(t *testing.T) {
	k := 3
	require.Lessf(t, getFloat64(), 1, "x=%d", k)
}

// Method form on *assert.Assertions (no leading t).
func CaseGreaterMethodForm(t *testing.T) {
	a := assert.New(t)
	a.Greater(getInt16(), 0)
}

// Identical default types -> no change (untyped 0 defaults to int).
func CaseGreaterIdenticalTypes(t *testing.T) {
	assert.Greater(t, getInt(), 0)
}

// Constant not representable in the operand's type -> no change (casting
// 1e9 to int16 would not compile / would change the comparison).
func CaseGreaterOverflow(t *testing.T) {
	assert.Greater(t, getInt16(), 1000000000)
}

// Idempotency: already-converted ordering operands must not be re-wrapped.
func CaseGreaterAlreadyCast(t *testing.T) {
	assert.Greater(t, getInt16(), int16(0))
}

// Element-comparison with a type-mismatched numeric element: the analyzer
// warns (to stderr) but does not rewrite Contains-family assertions.
func CaseContainsMismatch(t *testing.T) {
	assert.Contains(t, []int{1, 2, 3}, int64(2))
}

// Collection-vs-collection assertion with mismatched element types: the warning
// must compare the two collections' element types (int vs int64), not treat the
// second operand as a scalar.
func CaseElementsMatchMismatch(t *testing.T) {
	assert.ElementsMatch(t, []int{1, 2}, []int64{1, 2})
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
