package assertlint

import (
	"strings"
	"testing"
)

func TestManualAssertions(t *testing.T) {
	var err error
	var ok bool
	var x, y int

	// These should trigger assertlint
	if err != nil { // want "use assert.Nil instead of if \\+ t.Error/t.Fatal"
		t.Error("error")
	}

	if !ok { // want "use assert.True instead of if \\+ t.Error/t.Fatal"
		t.Error("not ok")
	}

	if x != y { // want "use assert.Equal instead of if \\+ t.Error/t.Fatal"
		t.Errorf("x != y")
	}

	if err == nil { // want "use assert.NotNil instead of if \\+ t.Error/t.Fatal"
		t.Error("expected error")
	}
}

func TestManualFatal(t *testing.T) {
	var err error

	// Fatal should use require
	if err != nil { // want "use require.Nil instead of if \\+ t.Error/t.Fatal"
		t.Fatal(err)
	}
}

func TestComparisonOperators(t *testing.T) {
	x := 5
	y := 10

	if x < y { // want "use assert.GreaterOrEqual instead of if \\+ t.Error/t.Fatal"
		t.Error("x should be >= y")
	}

	if x > y { // want "use assert.LessOrEqual instead of if \\+ t.Error/t.Fatal"
		t.Error("x should be <= y")
	}

	if x <= y { // want "use assert.Greater instead of if \\+ t.Error/t.Fatal"
		t.Error("x should be > y")
	}

	if x >= y { // want "use assert.Less instead of if \\+ t.Error/t.Fatal"
		t.Error("x should be < y")
	}
}

func TestNegatedComparisons(t *testing.T) {
	x := 5
	y := 10

	if !(x < y) { // want "use assert.Less instead of if \\+ t.Error/t.Fatal"
		t.Error("x should be < y")
	}

	if !(x == y) { // want "use assert.Equal instead of if \\+ t.Error/t.Fatal"
		t.Error("should be equal")
	}
}

func TestStringFunctions(t *testing.T) {
	s := "hello world"

	if !strings.Contains(s, "world") { // want "use assert.Contains instead of if \\+ t.Error/t.Fatal"
		t.Error("should contain world")
	}

	if strings.Contains(s, "foo") { // want "use assert.NotContains instead of if \\+ t.Error/t.Fatal"
		t.Error("should not contain foo")
	}

	if !strings.HasPrefix(s, "hello") { // want "use assert.True instead of if \\+ t.Error/t.Fatal"
		t.Error("should have prefix")
	}
}

func TestBoolIdent(t *testing.T) {
	ok := true

	if ok { // want "use assert.False instead of if \\+ t.Error/t.Fatal"
		t.Error("should be false")
	}
}

func TestFatalf(t *testing.T) {
	x := 1
	y := 2

	if x != y { // want "use require.Equal instead of if \\+ t.Error/t.Fatal"
		t.Fatalf("x=%d y=%d", x, y)
	}
}
