package bench

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// buildFailureStream is what `go test -json` emits when a test binary will not
// build. It is captured verbatim from a real run, because the shape is the
// whole point: the compiler's message rides `build-output` events that carry an
// ImportPath and no Package, and the stream filter used to drop them all.
const buildFailureStream = `{"ImportPath":"example.com/x/broken [example.com/x/broken.test]","Action":"build-output","Output":"# example.com/x/broken [example.com/x/broken.test]\n"}
{"ImportPath":"example.com/x/broken [example.com/x/broken.test]","Action":"build-output","Output":"link: mapping output file failed: no space left on device\n"}
{"ImportPath":"example.com/x/broken [example.com/x/broken.test]","Action":"build-fail"}
{"Action":"start","Package":"example.com/x/broken"}
{"Action":"output","Package":"example.com/x/broken","Output":"FAIL\texample.com/x/broken [build failed]\n"}
{"Action":"fail","Package":"example.com/x/broken","Elapsed":0}
`

func TestDiagnosticsKeepsTheBuildError(t *testing.T) {
	got := Diagnostics([]byte(buildFailureStream))
	assert.Contains(t, got, "no space left on device",
		"the one line that names the cause has to survive")
	assert.Contains(t, got, "# example.com/x/broken")
	assert.Contains(t, got, "FAIL\texample.com/x/broken [build failed]")
}

// A passing run's own output is not evidence about a failure, and it is what
// pushes the real error off the screen.
func TestDiagnosticsDropsWhatAPassingRunPrints(t *testing.T) {
	stream := `{"Action":"output","Package":"pkg","Output":"goos: linux\n"}
{"Action":"output","Package":"pkg","Output":"goarch: amd64\n"}
{"Action":"output","Package":"pkg","Output":"pkg: example.com/x\n"}
{"Action":"output","Package":"pkg","Output":"cpu: whatever\n"}
{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}
{"Action":"output","Package":"pkg","Output":"PASS\n"}
{"Action":"output","Package":"pkg","Output":"ok  \texample.com/x\t1.0s\n"}
`
	assert.Empty(t, Diagnostics([]byte(stream)))
}

// A benchmark that panics is the other way a run dies with no results, and its
// stack trace is the whole of what the user needs.
func TestDiagnosticsKeepsAPanickingBenchmark(t *testing.T) {
	stream := `{"Action":"output","Package":"pkg","Output":"goos: linux\n"}
{"Action":"output","Package":"pkg","Output":"panic: runtime error: index out of range [3] with length 2\n"}
{"Action":"output","Package":"pkg","Output":"\ngoroutine 7 [running]:\npkg.BenchmarkFoo(0xc0000b6000)\n"}
{"Action":"output","Package":"pkg","Output":"FAIL\texample.com/x\t0.1s\n"}
{"Action":"fail","Package":"pkg"}
`
	got := Diagnostics([]byte(stream))
	assert.Contains(t, got, "panic: runtime error")
	assert.Contains(t, got, "pkg.BenchmarkFoo")
	assert.NotContains(t, got, "goos:")
}

// Cutting is fine; cutting quietly is not. A wall of stack traces buries the
// cause, so the report is bounded — and says how much it left out.
func TestDiagnosticsSaysWhatItLeftOut(t *testing.T) {
	var b strings.Builder
	for i := range diagnosticLineCap + 50 {
		fmt.Fprintf(&b, `{"Action":"output","Package":"pkg","Output":"line %d\n"}`+"\n", i)
	}
	got := Diagnostics([]byte(b.String()))
	assert.Contains(t, got, "line 0")
	assert.Contains(t, got, fmt.Sprintf("... 50 more lines not shown (of %d)", diagnosticLineCap+50))
	assert.NotContains(t, got, fmt.Sprintf("line %d", diagnosticLineCap+49))
}

func TestDiagnosticsIgnoresGarbage(t *testing.T) {
	assert.Empty(t, Diagnostics(nil))
	assert.Empty(t, Diagnostics([]byte("not json at all\n{\n")))
}

// The bug this whole file exists for: a run whose test binary would not build
// used to report a bare "benchmarks failed" exit and nothing else, because
// only benchmark result lines ever reached the console.
func TestABuildFailureReportsWhyRatherThanJustFailing(t *testing.T) {
	mock := runner.NewMock()
	baseArgs := buildBenchArgs(Options{})
	jsonArgs := append([]string{baseArgs[0], "-json"}, baseArgs[1:]...)
	mock.SetResponse("go", jsonArgs, []byte(buildFailureStream), fmt.Errorf("exit status 1"))

	report, err := RunBenchmarks(mock, Options{})
	require.Error(t, err)
	assert.Nil(t, report)
	assert.Contains(t, err.Error(), "exit status 1")
	assert.Contains(t, err.Error(), "no space left on device",
		"a failure whose reason was on stdout must not be reported as an exit code alone")
}
