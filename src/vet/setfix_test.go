package vet

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis"
)

// TestSetFixRewritesEveryUse pins the rewrite. Each case is one whole file,
// so the output shows the declaration, the uses and the added import
// together: that is what has to compile.
func TestSetFixRewritesEveryUse(t *testing.T) {
	for _, c := range []struct {
		name     string
		analyzer *analysis.Analyzer
		body     string
		want     string
	}{
		{
			name:     "map made empty",
			analyzer: MapSetAnalyzer,
			body: "func f(names []string) int {\n" +
				"\tseen := make(map[string]bool)\n" +
				"\tfor _, n := range names {\n" +
				"\t\tif seen[n] {\n\t\t\tcontinue\n\t\t}\n" +
				"\t\tseen[n] = true\n" +
				"\t}\n" +
				"\tdelete(seen, \"x\")\n" +
				"\tfor n := range seen {\n\t\t_ = n\n\t}\n" +
				"\treturn len(seen)\n}",
			want: "func f(names []string) int {\n" +
				"\tseen := set.New[string]()\n" +
				"\tfor _, n := range names {\n" +
				"\t\tif seen.Contains(n) {\n\t\t\tcontinue\n\t\t}\n" +
				"\t\tseen.Add(n)\n" +
				"\t}\n" +
				"\tseen.Remove(\"x\")\n" +
				"\tfor n := range seen.All() {\n\t\t_ = n\n\t}\n" +
				"\treturn seen.Len()\n}",
		},
		{
			name:     "all-true map literal",
			analyzer: MapSetAnalyzer,
			body: "func f(k string) bool {\n" +
				"\thosts := map[string]bool{\"a\": true, \"b\": true}\n" +
				"\treturn hosts[k]\n}",
			want: "func f(k string) bool {\n" +
				"\thosts := set.Of[string](\"a\", \"b\")\n" +
				"\treturn hosts.Contains(k)\n}",
		},
		{
			name:     "bare map var keeps its type",
			analyzer: MapSetAnalyzer,
			body: "func f(k string) int {\n" +
				"\tvar seen map[string]bool\n" +
				"\tseen[k] = true\n" +
				"\treturn len(seen)\n}",
			want: "func f(k string) int {\n" +
				"\tvar seen set.Set[string]\n" +
				"\tseen.Add(k)\n" +
				"\treturn seen.Len()\n}",
		},
		{
			name:     "slice built then asked",
			analyzer: SliceSetAnalyzer,
			body: "func f(names []string, want string) bool {\n" +
				"\tseen := make([]string, 0)\n" +
				"\tfor _, n := range names {\n\t\tseen = append(seen, n)\n\t}\n" +
				"\treturn slices.Contains(seen, want) && len(seen) > 0\n}",
			want: "func f(names []string, want string) bool {\n" +
				"\tseen := set.New[string]()\n" +
				"\tfor _, n := range names {\n\t\tseen.Add(n)\n\t}\n" +
				"\treturn seen.Contains(want) && seen.Len() > 0\n}",
		},
		{
			name:     "slice literal in the lookup",
			analyzer: SliceSetAnalyzer,
			body: "func f(name string) bool {\n" +
				"\treturn slices.Contains([]string{\"linux\", \"darwin\"}, name)\n}",
			want: "func f(name string) bool {\n" +
				"\treturn set.Of[string](\"linux\", \"darwin\").Contains(name)\n}",
		},
		{
			name:     "slice ranged by value",
			analyzer: SliceSetAnalyzer,
			body: "func f(want string) int {\n" +
				"\ts := []string{\"a\"}\n" +
				"\ts = append(s, \"b\")\n" +
				"\tn := 0\n" +
				"\tfor _, v := range s {\n\t\tn += len(v)\n\t}\n" +
				"\tif slices.Contains(s, want) {\n\t\tn++\n\t}\n" +
				"\treturn n\n}",
			want: "func f(want string) int {\n" +
				"\ts := set.Of[string](\"a\")\n" +
				"\ts.Add(\"b\")\n" +
				"\tn := 0\n" +
				"\tfor v := range s.All() {\n\t\tn += len(v)\n\t}\n" +
				"\tif s.Contains(want) {\n\t\tn++\n\t}\n" +
				"\treturn n\n}",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			const header = "package main\n\nimport \"slices\"\n\nvar _ = slices.Contains\n\n"
			got := applySetFixes(t, c.analyzer, header+c.body+"\n")
			require.Contains(t, got, "\""+setPackage+"\"", "the rewrite must import the set package")
			require.Contains(t, got, c.want)
		})
	}
}

// TestSetFixLeavesUnspellableUsesAlone verifies a use with no set spelling
// blocks the whole variable. Half a rewrite does not compile, so the finding
// stays a diagnostic and the source is untouched.
func TestSetFixLeavesUnspellableUsesAlone(t *testing.T) {
	for _, c := range []struct {
		name     string
		analyzer *analysis.Analyzer
		body     string
	}{
		{
			name:     "map handed to a helper",
			analyzer: MapSetAnalyzer,
			body: "func f(k string) int {\n" +
				"\tseen := map[string]bool{\"a\": true}\n" +
				"\tseen[k] = true\n" +
				"\treturn count(seen)\n}\n\n" +
				"func count(m map[string]bool) int { return len(m) }",
		},
		{
			name:     "slice returned to the caller",
			analyzer: SliceSetAnalyzer,
			body: "func f(in []string) []string {\n" +
				"\tvar out []string\n" +
				"\tfor _, v := range in {\n" +
				"\t\tif !slices.Contains(out, v) {\n\t\t\tout = append(out, v)\n\t\t}\n\t}\n" +
				"\treturn out\n}",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			const header = "package main\n\nimport \"slices\"\n\nvar _ = slices.Contains\n\n"
			src := header + c.body + "\n"
			require.Empty(t, setFixesFor(t, c.analyzer, src), "no file may be rewritten")
		})
	}
}

// applySetFixes runs the analyzer and renders what its fixes produce.
func applySetFixes(t *testing.T, analyzer *analysis.Analyzer, src string) string {
	t.Helper()
	fixes := setFixesFor(t, analyzer, src)
	require.Len(t, fixes, 1, "one file, one set of fixes")
	var buf bytes.Buffer
	require.NoError(t, fixes[0].Fprint(&buf))
	return string(canonicalizeGoSource(buf.Bytes()))
}

// setFixesFor type-checks src and returns the analyzer's fixes. The package
// is named for a directory holding no test file, so a package-level variable
// is fixable too.
func setFixesFor(t *testing.T, analyzer *analysis.Analyzer, src string) []*ASTFixes {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, t.TempDir()+"/main.go", src, parser.ParseComments)
	require.NoError(t, err)

	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	conf := types.Config{Error: func(error) {}}
	pkg, _ := conf.Check("main", fset, []*ast.File{file}, info)

	pass := &analysis.Pass{
		Analyzer:  analyzer,
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		Report:    func(analysis.Diagnostic) {},
		TypesInfo: info,
	}
	result, err := analyzer.Run(pass)
	require.NoError(t, err)
	fixes, _ := result.([]*ASTFixes)
	return fixes
}
