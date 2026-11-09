package vet

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cpaShapesCode holds the if shapes the CLIProxyAPI tests carried when the
// fixer rewrote them into code that did not compile. Otherwise,
// cpaShapesCode holds that asserted the opposite of the original check.
// Each test function is one shape.
const cpaShapesCode = `package main

import (
	"errors"
	"testing"
)

type registry struct{}

func (registry) TranslateStream(raw []byte) [][]byte { return nil }

func (registry) TranslateNonStream(raw []byte) []byte { return nil }

type options struct{ Metadata map[string]any }

type auth struct{ ID string }

func pick() (*auth, error) { return &auth{ID: "a"}, nil }

func valid(sig string) bool { return sig != "" }

func capacity(n int) int { return n }

func lookupPtr() *int { return nil }

func run() error { return errors.New("x") }

func pair() (int, error) { return 0, nil }

type myErr struct{}

func (*myErr) Error() string { return "mine" }

func mine() *myErr { return nil }

func cached() (string, bool) { return "", false }

func joined(parts []string) bool { return len(parts) > 1 }

func TestInitVarReadByMessage(t *testing.T) {
	if got, ok := cached(); ok {
		t.Fatalf("cached = %q, true; want a miss", got)
	}
}

func TestSpreadArgument(t *testing.T) {
	core := []string{"a"}
	if joined(append(append([]string{}, core...), "b")) {
		t.Fatal("accepted")
	}
}

func TestTranslateNonErrorResult(t *testing.T) {
	r := registry{}
	if got := r.TranslateStream([]byte("{}")); got != nil {
		t.Errorf("TranslateStream leaked %s", got)
	}
}

func TestMetadataAnyValue(t *testing.T) {
	first := options{Metadata: map[string]any{}}
	if got := first.Metadata["lcp"]; got != nil {
		t.Fatalf("unexpectedly assigned LCP affinity ID: %#v", got)
	}
}

func TestMetadataCompound(t *testing.T) {
	first := options{Metadata: map[string]any{}}
	if got := first.Metadata["lcp"]; got == nil || got == "" {
		t.Fatalf("did not publish an LCP affinity session identity: %#v", got)
	}
}

func TestLoopIndexOnlyInMessages(t *testing.T) {
	want := []string{"a", "b"}
	for i, id := range want {
		got, err := pick()
		if err != nil {
			t.Fatalf("Pick() #%d error = %v", i, err)
		}
		if got == nil {
			t.Fatalf("Pick() #%d auth = nil", i)
		}
		if got.ID != id {
			t.Fatalf("Pick() #%d auth.ID = %q, want %q", i, got.ID, id)
		}
	}
}

func TestFieldReadInMessage(t *testing.T) {
	a := &auth{ID: "a"}
	if capacity(1) != 1 {
		t.Errorf("auth %s", a.ID)
	}
}

func TestLoopIndexOneMessage(t *testing.T) {
	samples := []string{"a"}
	for i, sig := range samples {
		if !valid(sig) {
			t.Fatalf("sample %d invalid", i)
		}
	}
}

func TestVarReadByTwoMessages(t *testing.T) {
	name := "x"
	if capacity(1) != 1 {
		t.Errorf("%s: one", name)
	}
	if capacity(2) != 2 {
		t.Errorf("%s: two", name)
	}
}

func TestTypedNilPointer(t *testing.T) {
	if p := lookupPtr(); p != nil {
		t.Error("p set")
	}
	if e := mine(); e != nil {
		t.Error("mine set")
	}
}

func TestErrorExpected(t *testing.T) {
	err := run()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestNilOnTheLeft(t *testing.T) {
	m := map[string]int{}
	if nil == m {
		t.Error("nil map")
	}
}

func TestErrorFromTwoResults(t *testing.T) {
	if _, err := pair(); err != nil {
		t.Fatal(err)
	}
}
`

// cpaImportsCode is a file whose strings and reflect imports are read only
// by checks the fixer rewrites. After the rewrite neither import has a use.
const cpaImportsCode = `package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestImportsLeftUnused(t *testing.T) {
	s := "hello world"
	if !strings.Contains(s, "world") {
		t.Error("missing world")
	}
	if !reflect.DeepEqual([]int{1}, []int{1}) {
		t.Errorf("slices differ")
	}
}
`

// TestAssertLintCLIProxyAPIShapes runs the fixer over the CLIProxyAPI shapes.
// The result must type-check, and each rewrite must assert what the if
// checked. An if with no exact assertion stays an if.
func TestAssertLintCLIProxyAPIShapes(t *testing.T) {
	t.Serial("vetSemantic loads the module from the working directory")
	stub, err := filepath.Abs(filepath.Join("testdata", "src", "testifystub"))
	require.NoError(t, err)

	dir := t.TempDir()
	shapes := filepath.Join(dir, "shapes_test.go")
	imports := filepath.Join(dir, "imports_test.go")
	require.NoError(t, os.WriteFile(shapes, []byte(cpaShapesCode), 0644))
	require.NoError(t, os.WriteFile(imports, []byte(cpaImportsCode), 0644))
	gomod := "module testmod\n\ngo 1.21\n\nrequire github.com/stretchr/testify v1.9.0\n\nreplace github.com/stretchr/testify => " + stub + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0644))

	t.Chdir(dir)
	initGitRepo(t, dir)

	changed, err := vetSemantic("./...", NewEditor(true), nil)
	require.NoError(t, err)
	assert.True(t, changed)

	shapesOut, err := os.ReadFile(shapes)
	require.NoError(t, err)
	importsOut, err := os.ReadFile(imports)
	require.NoError(t, err)
	got := string(shapesOut)

	build := exec.Command("go", "vet", "./...")
	build.Dir = dir
	out, err := build.CombinedOutput()
	require.NoError(t, err, "the rewritten files do not compile:\n%s\n%s\n%s", out, got, importsOut)

	fn := func(name string) string { return funcSource(t, got, name) }

	t.Run("a non-error result is checked with Nil, never NoError", func(t *testing.T) {
		src := fn("TestTranslateNonErrorResult")
		assert.Contains(t, src, `got := r.TranslateStream([]byte("{}"))`)
		assert.Contains(t, src, "assert.Nil(t, got)")
		assert.NotContains(t, src, "NoError")
	})
	t.Run("an any value has no exact nil assertion and stays an if", func(t *testing.T) {
		assert.Contains(t, fn("TestMetadataAnyValue"), `if got := first.Metadata["lcp"]; got != nil {`)
	})
	t.Run("a compound condition keeps its meaning", func(t *testing.T) {
		src := fn("TestMetadataCompound")
		assert.Contains(t, src, `got := first.Metadata["lcp"]`)
		assert.Contains(t, src, `require.False(t, got == nil || got == "")`)
		assert.NotContains(t, src, "NoError")
	})
	t.Run("a loop index only dropped messages read becomes _", func(t *testing.T) {
		src := fn("TestLoopIndexOnlyInMessages")
		assert.Contains(t, src, "for _, id := range want {")
		assert.Contains(t, src, "require.NoError(t, err)")
		assert.Contains(t, src, "require.NotNil(t, got)")
		assert.Contains(t, src, "require.Equal(t, id, got.ID)\n")
	})
	t.Run("a message that reads a field is not evaluated on the passing path", func(t *testing.T) {
		assert.Contains(t, fn("TestFieldReadInMessage"), `t.Errorf("auth %s", a.ID)`)
	})
	t.Run("a message that is the only reader of a range variable stays", func(t *testing.T) {
		assert.Contains(t, fn("TestLoopIndexOneMessage"), `require.True(t, valid(sig), "sample %d invalid", i)`)
	})
	t.Run("a variable two messages read keeps one of them", func(t *testing.T) {
		src := fn("TestVarReadByTwoMessages")
		assert.Contains(t, src, "assert.Equal(t, 1, capacity(1))\n")
		assert.Contains(t, src, `assert.Equal(t, 2, capacity(2), "%s: two", name)`)
	})
	t.Run("a pointer, even one to an error type, is checked with Nil", func(t *testing.T) {
		src := fn("TestTypedNilPointer")
		assert.Contains(t, src, "assert.Nil(t, p)")
		assert.Contains(t, src, "assert.Nil(t, e)")
	})
	t.Run("an error that must be set is checked with Error", func(t *testing.T) {
		assert.Contains(t, fn("TestErrorExpected"), "require.Error(t, err)")
	})
	t.Run("nil on the left names the other operand", func(t *testing.T) {
		assert.Contains(t, fn("TestNilOnTheLeft"), "assert.NotNil(t, m)")
	})
	t.Run("an error beside another result is hoisted, not passed whole", func(t *testing.T) {
		src := fn("TestErrorFromTwoResults")
		assert.Contains(t, src, "_, err := pair()")
		assert.Contains(t, src, "require.NoError(t, err)")
	})
	t.Run("a hoisted init keeps the message that is the only reader of its name", func(t *testing.T) {
		src := fn("TestInitVarReadByMessage")
		assert.Contains(t, src, "got, ok := cached()")
		assert.Contains(t, src, `require.False(t, ok, "cached = %q, true; want a miss", got)`)
	})
	t.Run("a spread argument keeps its ellipsis", func(t *testing.T) {
		assert.Contains(t, fn("TestSpreadArgument"), `require.False(t, joined(append(append([]string{}, core...), "b")))`)
	})
	t.Run("imports the rewrite left unused are removed", func(t *testing.T) {
		src := string(importsOut)
		assert.NotContains(t, src, `"strings"`)
		assert.NotContains(t, src, `"reflect"`)
		assert.Contains(t, src, `assert.Contains(t, s, "world")`)
		assert.Contains(t, src, "assert.Equal(t, []int{1}, []int{1})")
	})
}
