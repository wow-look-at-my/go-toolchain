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

// Nil/NotNil: the fixer emits these for `if x == nil` / `if x != nil`, which
// is the shape a hoisted if-init assertion takes.
func Nil(t TestingT, object interface{}, msgAndArgs ...interface{}) bool { return true }

func NotNil(t TestingT, object interface{}, msgAndArgs ...interface{}) bool { return true }

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

// Ordering assertions: upstream routes these through compareTwoValues, which
// requires both operands to have the same kind.
func Greater(t TestingT, e1, e2 interface{}, msgAndArgs ...interface{}) bool { return true }

func Greaterf(t TestingT, e1, e2 interface{}, msg string, args ...interface{}) bool { return true }

func GreaterOrEqual(t TestingT, e1, e2 interface{}, msgAndArgs ...interface{}) bool { return true }

func Less(t TestingT, e1, e2 interface{}, msgAndArgs ...interface{}) bool { return true }

func LessOrEqual(t TestingT, e1, e2 interface{}, msgAndArgs ...interface{}) bool { return true }

func Contains(t TestingT, s, contains interface{}, msgAndArgs ...interface{}) bool { return true }

func ElementsMatch(t TestingT, listA, listB interface{}, msgAndArgs ...interface{}) bool { return true }

// Assertions is the method-form receiver: a.Equal(expected, actual).
type Assertions struct{}

func New(t TestingT) *Assertions { return &Assertions{} }

func (a *Assertions) Equal(expected, actual interface{}, msgAndArgs ...interface{}) bool { return true }

func (a *Assertions) Equalf(expected, actual interface{}, msg string, args ...interface{}) bool {
	return true
}

func (a *Assertions) Greater(e1, e2 interface{}, msgAndArgs ...interface{}) bool { return true }

// True/False: the assertnorm fixtures assert on booleans.
func True(t TestingT, value bool, msgAndArgs ...interface{}) bool { return true }

func False(t TestingT, value bool, msgAndArgs ...interface{}) bool { return true }
