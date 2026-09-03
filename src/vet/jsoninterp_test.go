package vet

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"
)

// TestJSONInterpAnalyzer runs the fixture: every shape the check reports, and
// every neighbouring shape it must leave alone.
func TestJSONInterpAnalyzer(t *testing.T) {
	t.Serial() // See TestBannedOutputAnalyzer.
	testdata, err := filepath.Abs("testdata")
	require.NoError(t, err)
	analysistest.Run(t, testdata, JSONInterpAnalyzer, "jsoninterp")
}

// TestJSONInterpSeverityFollowsTheModule verifies the split the set checks
// carry: the remedy is the standard library either way, but only an org module
// is bound by this repo's conventions, so only there does a finding fail.
func TestJSONInterpSeverityFollowsTheModule(t *testing.T) {
	t.Serial()
	const src = `package main

import "fmt"

func main() { _ = fmt.Sprintf("{\"sha\":%q}", "abc") }
`
	cases := []struct {
		name        string
		module      *analysis.Module
		wantReports int
		wantWarns   int64
	}{
		{"org module fails", &analysis.Module{Path: "github.com/wow-look-at-my/go-toolchain"}, 1, 0},
		{"PazerOP module fails", &analysis.Module{Path: "github.com/PazerOP/tool"}, 1, 0},
		{"third-party module warns", &analysis.Module{Path: "example.com/consumer"}, 0, 1},
		{"nil module fails", nil, 1, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetJSONInterpWarnings()
			before := logger.WarnCount()
			reports := runJSONInterpOnSource(t, src, c.module)
			require.Len(t, reports, c.wantReports)
			require.Equal(t, before+c.wantWarns, logger.WarnCount())
		})
	}
}

// TestJSONInterpReportsEachDocumentOnce pins that a chain of concatenations is
// a single document, not a finding per operand.
func TestJSONInterpReportsEachDocumentOnce(t *testing.T) {
	const src = `package main

func body(a, b string) string { return "{\"a\":\"" + a + "\",\"b\":\"" + b + "\"}" }

func main() { _ = body("x", "y") }
`
	reports := runJSONInterpOnSource(t, src, nil)
	require.Len(t, reports, 1)
	assert.Contains(t, reports[0].Message, "JSON built by concatenation")
}

// TestJSONInterpLeavesOtherTextAlone pins the shapes that must stay silent.
// Each shape either carries no value or is not a JSON document, and a check
// that cries wolf on ordinary formatting is a check nobody reads.
func TestJSONInterpLeavesOtherTextAlone(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
	}{
		{"prose quoting json", `_ = fmt.Sprintf("expected {\"ok\":true}, got %s", s)`},
		{"static document", `_ = "{\"ok\":true}"`},
		{"two literals", `_ = "{\"ok\":" + "true}"`},
		{"braces with no string", `_ = fmt.Sprintf("{%s}", s)`},
		{"bracketed tag", `_ = fmt.Sprintf("[%s] %s", s, s)`},
		{"key and value prose", `_ = fmt.Sprintf("%s: %d", s, 1)`},
		{"percent literal", `_ = fmt.Sprintf("{\"pct\":100%%}")`},
		{"a css rule", `_ = fmt.Sprintf(".btn{color:%s}", s)`},
		{"a shell expansion", `_ = fmt.Sprintf("${%s}", s)`},
		{"another package", `_ = log.Sprintf("{\"sha\":%q}", s)`},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := "package main\n\nimport (\n\"fmt\"\n\"log\"\n)\n\nvar _, _ = fmt.Sprintf, log.Println\n\nfunc main() {\ns := \"x\"\n_ = s\n" + c.body + "\n}\n"
			require.Empty(t, runJSONInterpOnSource(t, src, nil))
		})
	}
}

// TestJSONInterpReadsTemplates pins that a template is judged on the document
// it renders: an action makes it interpolation, and no action makes it static.
func TestJSONInterpReadsTemplates(t *testing.T) {
	for _, c := range []struct {
		name string
		body string
		want int
	}{
		{"json template", "template.New(\"b\").Parse(`{\"name\":\"{{.Name}}\"}`)", 1},
		{"static json template", "template.New(\"b\").Parse(`{\"name\":\"fixed\"}`)", 0},
		{"html template", "template.New(\"b\").Parse(`<p>{{.Name}}</p>`)", 0},
		{"unclosed action", "template.New(\"b\").Parse(`{\"name\":\"{{.Name`)", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := "package main\n\nimport \"text/template\"\n\nvar _ = template.New\n\nfunc main() {\n_, _ = " + c.body + "\n}\n"
			require.Len(t, runJSONInterpOnSource(t, src, nil), c.want)
		})
	}
}

// runJSONInterpOnSource type-checks src and returns what the analyzer reports.
// The imports stay unresolved: supplying them means building export data
// up front, and the analyzer reads the package off the selector when the type
// checker could not resolve it.
func runJSONInterpOnSource(t *testing.T, src string, module *analysis.Module) []analysis.Diagnostic {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "/consumer/main.go", src, parser.ParseComments)
	require.NoError(t, err)

	info := &types.Info{
		Types: map[ast.Expr]types.TypeAndValue{},
		Defs:  map[*ast.Ident]types.Object{},
		Uses:  map[*ast.Ident]types.Object{},
	}
	conf := types.Config{Error: func(error) {}}
	pkg, _ := conf.Check("main", fset, []*ast.File{file}, info)

	var reports []analysis.Diagnostic
	pass := &analysis.Pass{
		Analyzer:  JSONInterpAnalyzer,
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		Report:    func(d analysis.Diagnostic) { reports = append(reports, d) },
		Module:    module,
		TypesInfo: info,
	}
	_, err = runJSONInterp(pass)
	require.NoError(t, err)
	return reports
}
