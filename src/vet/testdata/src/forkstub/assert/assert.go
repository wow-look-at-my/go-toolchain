// Package assert is a minimal hermetic stand-in for the wow-look-at-my/testify
// fork's assert package, used only so the vendor-consistency test can build a
// vendored module on the fork without a network dependency. After the rewriter
// runs, imports point at the stretchr stub instead.
package assert

// TestingT is the subset of *testing.T that testify assertions accept.
type TestingT interface {
	Errorf(format string, args ...interface{})
}

func Equal(t TestingT, expected, actual interface{}, msgAndArgs ...interface{}) bool { return true }

func True(t TestingT, value bool, msgAndArgs ...interface{}) bool { return value }
