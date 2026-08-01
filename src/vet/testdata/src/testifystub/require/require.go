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

// Nil/NotNil: the fixer emits these for `if x == nil` / `if x != nil`, which
// is the shape a hoisted if-init assertion takes.
func Nil(t TestingT, object interface{}, msgAndArgs ...interface{}) bool { return true }

func NotNil(t TestingT, object interface{}, msgAndArgs ...interface{}) bool { return true }

func Equalf(t TestingT, expected, actual interface{}, msg string, args ...interface{}) bool {
	return true
}

func NotEqual(t TestingT, expected, actual interface{}, msgAndArgs ...interface{}) bool { return true }

// Ordering assertions mirror the assert stub.
func Less(t TestingT, e1, e2 interface{}, msgAndArgs ...interface{}) bool { return true }

func Lessf(t TestingT, e1, e2 interface{}, msg string, args ...interface{}) bool { return true }

// Assertions is the method-form receiver: a.Equal(expected, actual).
type Assertions struct{}

func New(t TestingT) *Assertions { return &Assertions{} }

func (a *Assertions) Equal(expected, actual interface{}, msgAndArgs ...interface{}) bool { return true }
