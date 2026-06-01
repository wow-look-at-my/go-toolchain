package vet

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
	"golang.org/x/tools/go/analysis/analysistest"
)

// applyCastFixtures runs the analyzer over the testifycast fixture module and
// returns the rewritten source of every file that had fixes applied.
func applyCastFixtures(t *testing.T) string {
	t.Helper()
	// The fixture is a self-contained module (with a local replace to the stub
	// testify), so load it module-mode: point analysistest at the module root
	// with pattern ".", mirroring the assertnorm test.
	dir, err := filepath.Abs(filepath.Join("testdata", "src", "testifycast"))
	require.NoError(t, err)

	results := analysistest.Run(t, dir, TestifyCastAnalyzer, ".")

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
		}
	}
	return out.String()
}

func TestTestifyCastAnalyzer(t *testing.T) {
	out := applyCastFixtures(t)
	require.NotEmpty(t, out, "expected some fixes to be applied")

	// Cases that MUST gain a conversion.
	want := []string{
		// Case 1: untyped literal vs float64 call result.
		"assert.Equal(t, float64(0), getFloat64())",
		// Case 2: operands swapped — literal sits in the actual slot.
		"assert.Equal(t, getFloat64(), float64(0))",
		// Case 3: Equalf, format string and args left intact.
		`require.Equalf(t, float64(0), getFloat64(), "x=%d", k)`,
		// Case 4: *Assertions method form (no leading t).
		"a.Equal(float64(0), getFloat64())",
		// Case 5: typed int32 vs int64 — expected wrapped.
		"assert.Equal(t, int64(getInt32()), getInt64())",
		// task uint example — actual literal wrapped.
		"require.Equal(t, getUint(), uint(10))",
		// NotEqual handled identically.
		"assert.NotEqual(t, float64(0), getFloat64())",
		// Case 8: numeric named type Celsius vs float64.
		"assert.Equal(t, float64(getCelsius()), getFloat64())",
		// Rule 5: non-numeric same-kind named type Name vs string.
		`assert.Equal(t, string(getName()), "")`,
	}
	for _, w := range want {
		assert.Contains(t, out, w)
	}

	// Cases that MUST be left exactly as written.
	unchanged := []string{
		"assert.Equal(t, getInt(), getInt())",    // identical types
		`assert.Equal(t, "x", []byte("x"))`,      // non-numeric mismatch
		"assert.Equal(t, 1.5, getInt())",         // fractional truncation
		"x.Equal(0, getFloat64())",               // non-testify Equal
		"assert.EqualValues(t, 0, getFloat64())", // EqualValues untouched
	}
	for _, u := range unchanged {
		assert.Contains(t, out, u)
	}

	// Idempotency: already-converted operands must never be double-wrapped.
	assert.NotContains(t, out, "float64(float64(")
	assert.NotContains(t, out, "int64(int64(")
	assert.NotContains(t, out, "uint(uint(")
}

func TestIsForkNumeric(t *testing.T) {
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
	assert.True(t, isUntypedLiteral(&ast.BasicLit{Kind: token.INT, Value: "0"}))
	assert.True(t, isUntypedLiteral(&ast.BasicLit{Kind: token.FLOAT, Value: "1.5"}))
	assert.False(t, isUntypedLiteral(&ast.Ident{Name: "x"}))
	assert.False(t, isUntypedLiteral(&ast.CallExpr{Fun: &ast.Ident{Name: "int32"}}))
}

func TestConstRepresentable(t *testing.T) {
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
	// Fractional value cannot become an integer (rule 10).
	assert.False(t, constRepresentable(mkFloat(1.5), intT))
	// Whole-valued float can.
	assert.True(t, constRepresentable(mkFloat(2.0), intT))
	// Any finite numeric constant is representable as a float.
	assert.True(t, constRepresentable(mkInt(0), float64T))
	assert.True(t, constRepresentable(mkFloat(1.5), float32T))
	// Unknown / nil are not representable.
	assert.False(t, constRepresentable(nil, intT))
	assert.False(t, constRepresentable(constant.MakeUnknown(), intT))
}

func TestImportsUpstreamTestify(t *testing.T) {
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
