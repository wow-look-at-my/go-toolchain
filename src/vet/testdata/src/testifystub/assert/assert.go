// Package assert is a minimal hermetic stand-in for
// github.com/stretchr/testify/assert. It exists only so the testifycast
// analyzer fixtures type-check with the correct package path
// (github.com/stretchr/testify/assert) without a network dependency. The
// signatures mirror upstream closely enough for the analyzer's purposes:
// package-level funcs take a leading TestingT; *Assertions methods do not.
package assert

// TestingT is the subset of *testing.T that testify assertions accept.
type TestingT interface {
	Errorf(format string, args ...interface{})
}

func Equal(t TestingT, expected, actual interface{}, msgAndArgs ...interface{}) bool { return true }

func Equalf(t TestingT, expected, actual interface{}, msg string, args ...interface{}) bool {
	return true
}

func NotEqual(t TestingT, expected, actual interface{}, msgAndArgs ...interface{}) bool { return true }

func NotEqualf(t TestingT, expected, actual interface{}, msg string, args ...interface{}) bool {
	return true
}

// EqualValues already does convertible comparison upstream, so the analyzer
// must leave it alone.
func EqualValues(t TestingT, expected, actual interface{}, msgAndArgs ...interface{}) bool {
	return true
}

func Contains(t TestingT, s, contains interface{}, msgAndArgs ...interface{}) bool { return true }

func ElementsMatch(t TestingT, listA, listB interface{}, msgAndArgs ...interface{}) bool { return true }

// Assertions is the method-form receiver: a.Equal(expected, actual).
type Assertions struct{}

func New(t TestingT) *Assertions { return &Assertions{} }

func (a *Assertions) Equal(expected, actual interface{}, msgAndArgs ...interface{}) bool { return true }

func (a *Assertions) Equalf(expected, actual interface{}, msg string, args ...interface{}) bool {
	return true
}
