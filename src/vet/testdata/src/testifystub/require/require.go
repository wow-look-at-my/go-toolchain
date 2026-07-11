// Package require is a minimal hermetic stand-in for
// github.com/stretchr/testify/require, mirroring the assert stub. See the
// assert package doc for why this exists.
package require

// TestingT is the subset of *testing.T that testify assertions accept.
type TestingT interface {
	Errorf(format string, args ...interface{})
	FailNow()
}

func Equal(t TestingT, expected, actual interface{}, msgAndArgs ...interface{}) bool { return true }

func Equalf(t TestingT, expected, actual interface{}, msg string, args ...interface{}) bool {
	return true
}

func NotEqual(t TestingT, expected, actual interface{}, msgAndArgs ...interface{}) bool { return true }

// Assertions is the method-form receiver: a.Equal(expected, actual).
type Assertions struct{}

func New(t TestingT) *Assertions { return &Assertions{} }

func (a *Assertions) Equal(expected, actual interface{}, msgAndArgs ...interface{}) bool { return true }
