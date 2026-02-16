package assertlint

import "testing"

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
