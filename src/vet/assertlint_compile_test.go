package vet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertlintCompileCode holds the if shapes the sglang schedsim tests carried
// when the fixer rewrote them into code that did not compile. Each test
// function is one shape; the test below reads each function's rewrite apart.
const assertlintCompileCode = `package main

import (
	"errors"
	"testing"
)

type result struct {
	GeneratedTokens float64
}

func capacity(n int) int { return n }

func lines(s string) []string { return []string{s} }

func run(args []string) error {
	if len(args) == 0 {
		return errors.New("x")
	}
	return nil
}

func pair() (int, error) { return 0, nil }

func intField() (int, bool, error) { return 5, true, nil }

func floatField() (float64, error) { return 2.5, nil }

func finish(n int) []int { return make([]int, n) }

func build(n int) ([]int, error) { return make([]int, n), nil }

func lookup(n int) func() error {
	if n == 0 {
		return nil
	}
	return func() error { return nil }
}

func TestSameNameTwice(t *testing.T) {
	if got := capacity(400); got != 400 {
		t.Errorf("Capacity() = %d, want 400", got)
	}
	if got := capacity(100); got != 100 {
		t.Errorf("Capacity() = %d, want 100", got)
	}
}

func TestSameNameNewType(t *testing.T) {
	if got := capacity(0); got != 0 {
		t.Errorf("got %d", got)
	}
	if got := lines("a"); len(got) != 1 {
		t.Errorf("got %q", got)
	}
}

func TestNameDeclaredLater(t *testing.T) {
	if got := capacity(1); got != 1 {
		t.Errorf("got %d", got)
	}
	got := lines("b")
	if len(got) != 1 {
		t.Errorf("got %q", got)
	}
}

func TestErrRepeated(t *testing.T) {
	if _, err := pair(); err != nil {
		t.Errorf("first: %v", err)
	}
	if _, err := pair(); err != nil {
		t.Errorf("second: %v", err)
	}
}

func TestErrorsChained(t *testing.T) {
	if err := run(nil); err == nil || err.Error() != "x" {
		t.Errorf("no args: %v", err)
	}
	if err := run([]string{"a"}); err == nil {
		t.Error("args accepted")
	}
}

func TestMixedTypes(t *testing.T) {
	if v, ok, _ := intField(); v != 5 || !ok {
		t.Error("intField")
	}
	if v, _ := floatField(); v != 2.5 {
		t.Error("floatField")
	}
}

func TestLaterMultiDecl(t *testing.T) {
	var err error
	if late := finish(1); len(late) != 1 {
		t.Errorf("late %v", late)
	}
	late, err := build(2)
	if err != nil {
		t.Fatal(err)
	}
	_ = late
}

func TestNegatedParen(t *testing.T) {
	a, b := []float64{1, 2}, 1.5
	for i := 1; i < len(a); i++ {
		if !(a[i] > a[i-1]) {
			t.Errorf("did not grow")
		}
		if !(a[i] >= b) {
			t.Errorf("below")
		}
	}
	if !(a[1] > 1.5*a[0]) {
		t.Errorf("too small")
	}
}

func TestFloatAgainstZero(t *testing.T) {
	r := result{GeneratedTokens: 1}
	if r.GeneratedTokens <= 0 {
		t.Fatalf("generated nothing")
	}
	lo := 3.0
	if lo >= 8 {
		t.Fatalf("lo %v", lo)
	}
}

func TestContinueKept(t *testing.T) {
	for _, n := range []int{0, 1} {
		check := lookup(n)
		if check == nil {
			t.Errorf("no check for %d", n)
			continue
		}
		if err := check(); err != nil {
			t.Errorf("check: %v", err)
		}
	}
}

func TestMessageOnlyVar(t *testing.T) {
	name := "x"
	if capacity(1) != 1 {
		t.Errorf("%s", name)
	}
}

func TestWorkBeforeFailure(t *testing.T) {
	failed := false
	if capacity(1) != 1 {
		failed = true
		t.Error("bad")
	}
	_ = failed
}

func TestShadowOuterRead(t *testing.T) {
	err := errors.New("outer")
	for i := 0; i < 1; i++ {
		if _, err := pair(); err != nil {
			t.Fatal(err)
		}
		if err == nil {
			t.Error("outer err lost")
		}
	}
}

func TestReuseThenRead(t *testing.T) {
	n := capacity(3)
	if n := capacity(4); n != 4 {
		t.Error("four")
	}
	if n != 3 {
		t.Error("three")
	}
}
`

// funcSource returns the text of the top-level function name in src.
func funcSource(t *testing.T, src, name string) string {
	t.Helper()
	start := strings.Index(src, "func "+name+"(")
	require.GreaterOrEqual(t, start, 0, "no func %s in:\n%s", name, src)
	end := strings.Index(src[start+1:], "\nfunc ")
	if end < 0 {
		return src[start:]
	}
	return src[start : start+1+end]
}

// TestAssertLintRewriteCompiles runs the fixer over the shapes that broke the
// build and requires the result to type-check, compare what the if compared,
// and leave alone every if it cannot rewrite without changing the test.
func TestAssertLintRewriteCompiles(t *testing.T) {
	t.Serial()
	stub, err := filepath.Abs(filepath.Join("testdata", "src", "testifystub"))
	require.NoError(t, err)

	dir := t.TempDir()
	file := filepath.Join(dir, "shapes_test.go")
	require.NoError(t, os.WriteFile(file, []byte(assertlintCompileCode), 0644))
	gomod := "module testmod\n\ngo 1.21\n\nrequire github.com/stretchr/testify v1.9.0\n\nreplace github.com/stretchr/testify => " + stub + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644))

	t.Chdir(dir)
	initGitRepo(t, dir)

	changed, err := vetSemantic("./...", NewEditor(true), nil)
	require.NoError(t, err)
	assert.True(t, changed)

	content, err := os.ReadFile(file)
	require.NoError(t, err)
	got := string(content)

	build := exec.Command("go", "vet", "./...")
	build.Dir = dir
	out, err := build.CombinedOutput()
	require.NoError(t, err, "the rewritten file does not compile:\n%s\n%s", out, got)

	fn := func(name string) string { return funcSource(t, got, name) }

	t.Run("a second init of the same name and type reuses the first", func(t *testing.T) {
		src := fn("TestSameNameTwice")
		assert.Contains(t, src, "got := capacity(400)")
		assert.Contains(t, src, "got = capacity(100)")
		assert.Contains(t, src, "assert.Equal(t, 100, got)")
	})
	t.Run("a second init of the same name with a new type stays an if", func(t *testing.T) {
		src := fn("TestSameNameNewType")
		assert.Contains(t, src, "got := capacity(0)")
		assert.Contains(t, src, `if got := lines("a"); len(got) != 1 {`)
	})
	t.Run("a name the block declares later is not hoisted", func(t *testing.T) {
		src := fn("TestNameDeclaredLater")
		assert.Contains(t, src, "if got := capacity(1); got != 1 {")
		assert.Contains(t, src, "assert.Equal(t, 1, len(got))")
	})
	t.Run("repeated inits declare once and then assign", func(t *testing.T) {
		src := fn("TestErrRepeated")
		assert.Equal(t, 1, strings.Count(src, "_, err := pair()"), src)
		assert.Equal(t, 1, strings.Count(src, "_, err = pair()"), src)
	})
	t.Run("an error checked by two inits", func(t *testing.T) {
		src := fn("TestErrorsChained")
		assert.Contains(t, src, "err := run(nil)")
		assert.Contains(t, src, `err = run([]string{"a"})`)
		assert.Contains(t, src, "assert.NotNil(t, err)")
	})
	t.Run("a reused name with another type stays an if", func(t *testing.T) {
		src := fn("TestMixedTypes")
		assert.Contains(t, src, "v, ok, _ := intField()")
		assert.Contains(t, src, "if v, _ := floatField(); v != 2.5 {")
	})
	t.Run("a name declared later alongside an existing one is not hoisted", func(t *testing.T) {
		src := fn("TestLaterMultiDecl")
		assert.Contains(t, src, "if late := finish(1); len(late) != 1 {")
		assert.Contains(t, src, "late, err := build(2)")
	})
	t.Run("a negated parenthesized comparison keeps both operands", func(t *testing.T) {
		src := fn("TestNegatedParen")
		assert.Contains(t, src, "assert.Greater(t, a[i], a[i-1])")
		assert.Contains(t, src, "assert.GreaterOrEqual(t, a[i], b)")
		assert.Contains(t, src, "assert.Greater(t, a[1], 1.5*a[0])")
		assert.NotContains(t, src, "(a[i] > a[i-1]))")
	})
	t.Run("an untyped constant takes the other operand's type", func(t *testing.T) {
		src := fn("TestFloatAgainstZero")
		assert.Contains(t, src, "require.Greater(t, r.GeneratedTokens, float64(0))")
		assert.Contains(t, src, "require.Less(t, lo, float64(8))")
	})
	t.Run("a statement after t.Error stays behind the assertion", func(t *testing.T) {
		src := fn("TestContinueKept")
		assert.Contains(t, src, "if !assert.NotNil(t, check) {\n\t\t\tcontinue\n\t\t}")
		assert.Contains(t, src, "assert.NoError(t, check())")
	})
	t.Run("a variable only the message reads stays", func(t *testing.T) {
		assert.Contains(t, fn("TestMessageOnlyVar"), `t.Errorf("%s", name)`)
	})
	t.Run("work before the failure call stays", func(t *testing.T) {
		src := fn("TestWorkBeforeFailure")
		assert.Contains(t, src, "failed = true")
		assert.Contains(t, src, `t.Error("bad")`)
	})
	t.Run("an init shadowing a name the block reads later stays an if", func(t *testing.T) {
		src := fn("TestShadowOuterRead")
		assert.Contains(t, src, "if _, err := pair(); err != nil {")
		assert.Contains(t, src, "assert.NotNil(t, err)")
	})
	t.Run("an existing variable read after the if is not overwritten", func(t *testing.T) {
		src := fn("TestReuseThenRead")
		assert.Contains(t, src, "if n := capacity(4); n != 4 {")
		assert.Contains(t, src, "assert.Equal(t, 3, n)")
	})
}
