package vet

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis/analysistest"
)

// applyCastFixtures runs the analyzer over the testifycast fixture module and
// returns the rewritten source of every file that had fixes applied, anything
// the analyzer wrote to stderr (element-mismatch warnings), and the raw edit
// sets themselves (for asserting on recorded imports).
func applyCastFixtures(t *testing.T) (output, stderrText string, all []*CastEdits) {
	t.Helper()
	t.Serial() // analysistest chdirs into the fixture
	// Self-contained module with a stub testify replace; load it module-mode like assertnorm.
	dir, err := filepath.Abs(filepath.Join("testdata", "src", "testifycast"))
	require.NoError(t, err)

	// Pin non-GHA mode so the warning lands on stderr, not a stdout annotation.
	t.Setenv("GITHUB_ACTIONS", "")

	// Capture os.Stderr so we can assert on element-mismatch warnings.
	oldStderr := os.Stderr
	pr, pw, _ := os.Pipe()
	os.Stderr = pw
	captured := make(chan string, 1)
	go func() {
		var sb strings.Builder
		io.Copy(&sb, pr)
		captured <- sb.String()
	}()

	results := analysistest.Run(t, dir, TestifyCastAnalyzer, ".")

	pw.Close()
	os.Stderr = oldStderr
	stderrText = <-captured

	var out strings.Builder
	for _, r := range results {
		editsList, ok := r.Result.([]*CastEdits)
		if !ok {
			continue
		}
		for _, c := range editsList {
			if c == nil || len(c.Edits) == 0 {
				continue
			}
			src, err := os.ReadFile(c.Filename)
			require.NoError(t, err)
			out.Write(c.rendered(src))
			all = append(all, c)
		}
	}
	return out.String(), stderrText, all
}

func TestTestifyCastAnalyzer(t *testing.T) {
	t.Serial()
	out, stderr, all := applyCastFixtures(t)
	require.NotEmpty(t, out, "expected some fixes to be applied")

	// Cases that MUST gain a conversion.
	want := []string{
		// An untyped literal vs a float64 call result.
		"assert.Equal(t, float64(0), getFloat64())",
		// Operands swapped — literal sits in the actual slot.
		"assert.Equal(t, getFloat64(), float64(0))",
		// Equalf, format string and args left intact.
		`require.Equalf(t, float64(0), getFloat64(), "x=%d", k)`,
		// The *Assertions method form (no leading t).
		"a.Equal(float64(0), getFloat64())",
		// Typed int32 vs int64 — expected wrapped.
		"assert.Equal(t, int64(getInt32()), getInt64())",
		// task uint example — actual literal wrapped.
		"require.Equal(t, getUint(), uint(10))",
		// NotEqual handled identically.
		"assert.NotEqual(t, float64(0), getFloat64())",
		// A numeric named type Celsius vs float64.
		"assert.Equal(t, float64(getCelsius()), getFloat64())",
		// A non-numeric same-kind named type Name vs string.
		`assert.Equal(t, string(getName()), "")`,
		// Literal on the expected side: wrap into the named type, no numeric guard.
		`assert.Equal(t, Name(""), getName())`,
		// Cross-package named numeric type spelled with the import qualifier.
		"assert.Equal(t, time.Duration(0), getDuration())",
		// Dot-imported named type: spelled unqualified, not ".Duration".
		"assert.Equal(t, Duration(0), getDot())",
		// Missing import for the named type's package is recorded and added on fix.
		"assert.NotEqual(t, modes.Mode(0), getMode())",
		// Ordering assertions: an int16 field vs an untyped constant (compareTwoValues is kind-strict).
		"assert.Greater(t, getInt16(), int16(0))",
		// float64 vs an untyped constant — the go-font-renderer TestSuperRoundNegative shape.
		"assert.Less(t, getFloat64(), float64(0))",
		// Typed width mismatch — e1 wrapped.
		"assert.GreaterOrEqual(t, int64(getInt32()), getInt64())",
		// require package form.
		"require.Less(t, getInt16(), int16(100))",
		// f-variant, format string and args left intact.
		`require.Lessf(t, getFloat64(), float64(1), "x=%d", k)`,
		// *Assertions method form.
		"a.Greater(getInt16(), int16(0))",
	}
	for _, w := range want {
		assert.Contains(t, out, w)
	}
	// Dot-import conversion must not carry a stray "." qualifier.
	assert.NotContains(t, out, "(t, .Duration(0)")

	// Element-comparison mismatches (Contains, ElementsMatch) are warned, not rewritten.
	assert.Contains(t, stderr, "testifycast: warning")
	assert.Contains(t, stderr, "Contains")
	assert.Contains(t, stderr, "ElementsMatch")
	assert.Contains(t, out, "assert.Contains(t, []int{1, 2, 3}, int64(2))")

	// Cases that MUST be left exactly as written.
	unchanged := []string{
		"assert.Equal(t, getInt(), getInt())",       // identical types
		`assert.Equal(t, "x", []byte("x"))`,         // non-numeric mismatch
		"assert.Equal(t, 1.5, getInt())",            // fractional truncation
		"x.Equal(0, getFloat64())",                  // non-testify Equal
		"assert.EqualValues(t, 0, getFloat64())",    // EqualValues untouched
		"assert.Greater(t, getInt(), 0)",            // identical default types
		"assert.Greater(t, getInt16(), 1000000000)", // constant overflows int16
	}
	for _, u := range unchanged {
		assert.Contains(t, out, u)
	}

	// Idempotency: already-converted operands must never be double-wrapped.
	assert.NotContains(t, out, "float64(float64(")
	assert.NotContains(t, out, "int64(int64(")
	assert.NotContains(t, out, "uint(uint(")
	assert.NotContains(t, out, "int16(int16(")

	// notimported.go must record the import to add; every other file records none.
	var notImported *CastEdits
	for _, c := range all {
		if strings.HasSuffix(c.Filename, "notimported.go") {
			notImported = c
			continue
		}
		assert.Empty(t, c.neededImports(), c.Filename)
	}
	require.NotNil(t, notImported, "expected an edit set for notimported.go")
	require.Len(t, notImported.Edits, 1)
	assert.Equal(t, []string{"testifycast/modes"}, notImported.Edits[0].AddImports)
	assert.Equal(t, []string{"testifycast/modes"}, notImported.neededImports())
}

func TestIsForkNumeric(t *testing.T) {
	t.Serial()
	numeric := []types.BasicKind{
		types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr,
		types.Float32, types.Float64,
	}
	for _, k := range numeric {
		assert.True(t, isForkNumeric(types.Typ[k]), "kind %v should be numeric", k)
	}
	nonNumeric := []types.BasicKind{
		types.Bool, types.String, types.Complex64, types.Complex128,
	}
	for _, k := range nonNumeric {
		assert.False(t, isForkNumeric(types.Typ[k]), "kind %v should not be numeric", k)
	}
}

func TestIsUntypedLiteral(t *testing.T) {
	t.Serial()
	assert.True(t, isUntypedLiteral(&ast.BasicLit{Kind: token.INT, Value: "0"}))
	assert.True(t, isUntypedLiteral(&ast.BasicLit{Kind: token.FLOAT, Value: "1.5"}))
	assert.False(t, isUntypedLiteral(&ast.Ident{Name: "x"}))
	assert.False(t, isUntypedLiteral(&ast.CallExpr{Fun: &ast.Ident{Name: "int32"}}))
}

func TestConstRepresentable(t *testing.T) {
	t.Serial()
	intT := types.Typ[types.Int]
	int8T := types.Typ[types.Int8]
	uint8T := types.Typ[types.Uint8]
	float64T := types.Typ[types.Float64]
	float32T := types.Typ[types.Float32]

	mkInt := func(i int64) constant.Value { return constant.MakeInt64(i) }
	mkFloat := func(f float64) constant.Value { return constant.MakeFloat64(f) }

	// Integers within range.
	assert.True(t, constRepresentable(mkInt(0), intT))
	assert.True(t, constRepresentable(mkInt(127), int8T))
	assert.True(t, constRepresentable(mkInt(255), uint8T))
	// Integer overflow / sign violations.
	assert.False(t, constRepresentable(mkInt(256), int8T))
	assert.False(t, constRepresentable(mkInt(-1), uint8T))
	assert.False(t, constRepresentable(mkInt(256), uint8T))
	// A fractional value cannot become an integer.
	assert.False(t, constRepresentable(mkFloat(1.5), intT))
	// Whole-valued float can.
	assert.True(t, constRepresentable(mkFloat(2.0), intT))
	// Any finite numeric constant is representable as a float.
	assert.True(t, constRepresentable(mkInt(0), float64T))
	assert.True(t, constRepresentable(mkFloat(1.5), float32T))
	// Unknown / nil are not representable.
	assert.False(t, constRepresentable(nil, intT))
	assert.False(t, constRepresentable(constant.MakeUnknown(), intT))

	// Exercise every integer-kind range branch.
	assert.True(t, constRepresentable(mkInt(30000), types.Typ[types.Int16]))
	assert.False(t, constRepresentable(mkInt(40000), types.Typ[types.Int16]))
	assert.True(t, constRepresentable(mkInt(40000), types.Typ[types.Int32]))
	assert.True(t, constRepresentable(mkInt(60000), types.Typ[types.Uint16]))
	assert.False(t, constRepresentable(mkInt(70000), types.Typ[types.Uint16]))
	assert.True(t, constRepresentable(mkInt(100), types.Typ[types.Uint32]))
	assert.False(t, constRepresentable(mkInt(-1), types.Typ[types.Uint32]))
	assert.True(t, constRepresentable(mkInt(100), types.Typ[types.Int64]))
	assert.True(t, constRepresentable(mkInt(100), types.Typ[types.Uint64]))
	assert.True(t, constRepresentable(mkInt(100), types.Typ[types.Uint]))
	assert.False(t, constRepresentable(mkInt(-1), types.Typ[types.Uint]))
	assert.True(t, constRepresentable(mkInt(100), types.Typ[types.Uintptr]))
	// Complex target is out of scope -> not representable.
	assert.False(t, constRepresentable(mkInt(0), types.Typ[types.Complex128]))

	// Float target width matters: a float64 value too large for float32 must not cast.
	assert.True(t, constRepresentable(mkFloat(1e38), float32T))
	assert.False(t, constRepresentable(mkFloat(1e100), float32T))
	assert.True(t, constRepresentable(mkFloat(1e100), float64T))
}

func TestCastEditsApply(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	fp := filepath.Join(dir, "x.go")
	src := "package p\n\nvar _ = 0\n"
	require.NoError(t, os.WriteFile(fp, []byte(src), 0644))

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, fp, src, 0)
	require.NoError(t, err)

	var lit *ast.BasicLit
	ast.Inspect(f, func(n ast.Node) bool {
		if b, ok := n.(*ast.BasicLit); ok {
			lit = b
			return false
		}
		return true
	})
	require.NotNil(t, lit)

	ce := &CastEdits{
		Filename: fp,
		Fset:     fset,
		Edits:    []CastEdit{{Start: lit.Pos(), End: lit.End(), TypeName: "float64"}},
	}
	wrote, err := ce.Apply(NewEditor(true))
	require.NoError(t, err)
	require.True(t, wrote)

	got, err := os.ReadFile(fp)
	require.NoError(t, err)
	assert.Contains(t, string(got), "var _ = float64(0)")
}

// TestCastEditsApplyAddsImport verifies Apply adds the import an edit recorded
// (a conversion naming a package the file doesn't import must not leave the
// file unloadable), merged into the existing import block gofmt-canonically.
func TestCastEditsApplyAddsImport(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	fp := filepath.Join(dir, "x.go")
	src := "package p\n\nimport \"os\"\n\nvar _ = os.Getenv(\"\")\n\nvar _ = 0\n"
	require.NoError(t, os.WriteFile(fp, []byte(src), 0644))

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, fp, src, 0)
	require.NoError(t, err)

	var lit *ast.BasicLit
	ast.Inspect(f, func(n ast.Node) bool {
		if b, ok := n.(*ast.BasicLit); ok && b.Kind == token.INT {
			lit = b
			return false
		}
		return true
	})
	require.NotNil(t, lit)

	ce := &CastEdits{
		Filename: fp,
		Fset:     fset,
		Edits: []CastEdit{{
			Start:      lit.Pos(),
			End:        lit.End(),
			TypeName:   "fs.FileMode",
			AddImports: []string{"io/fs"},
		}},
	}
	wrote, err := ce.Apply(NewEditor(true))
	require.NoError(t, err)
	require.True(t, wrote)

	got, err := os.ReadFile(fp)
	require.NoError(t, err)
	assert.Contains(t, string(got), "var _ = fs.FileMode(0)")
	assert.Contains(t, string(got), `"io/fs"`)
}

// TestCastEditsApplyCheckMode verifies a check-mode (CI) editor records the
// pending conversion as a violation and does NOT rewrite the file.
func TestCastEditsApplyCheckMode(t *testing.T) {
	t.Serial()
	dir := t.TempDir()
	fp := filepath.Join(dir, "x.go")
	src := "package p\n\nvar _ = 0\n"
	require.NoError(t, os.WriteFile(fp, []byte(src), 0644))

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, fp, src, 0)
	require.NoError(t, err)

	var lit *ast.BasicLit
	ast.Inspect(f, func(n ast.Node) bool {
		if b, ok := n.(*ast.BasicLit); ok {
			lit = b
			return false
		}
		return true
	})
	require.NotNil(t, lit)

	ce := &CastEdits{
		Filename: fp,
		Fset:     fset,
		Edits:    []CastEdit{{Start: lit.Pos(), End: lit.End(), TypeName: "float64"}},
	}
	ed := NewEditor(false)
	wrote, err := ce.Apply(ed)
	require.NoError(t, err)
	assert.False(t, wrote)
	require.Error(t, ed.Err())

	got, err := os.ReadFile(fp)
	require.NoError(t, err)
	assert.Equal(t, src, string(got)) // check mode must not write
}

func TestImportsUpstreamTestify(t *testing.T) {
	t.Serial()
	withImport := func(path string) *ast.File {
		return &ast.File{Imports: []*ast.ImportSpec{
			{Path: &ast.BasicLit{Kind: token.STRING, Value: `"` + path + `"`}},
		}}
	}
	assert.True(t, importsUpstreamTestify(withImport("github.com/stretchr/testify/assert")))
	assert.True(t, importsUpstreamTestify(withImport("github.com/stretchr/testify/require")))
	assert.False(t, importsUpstreamTestify(withImport("github.com/wow-look-at-my/testify/assert")))
	assert.False(t, importsUpstreamTestify(withImport("fmt")))
}
