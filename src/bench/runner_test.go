package bench

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestBuildBenchArgsDefaults(t *testing.T) {
	opts := Options{}
	args := buildBenchArgs(opts)

	expected := []string{"test", "-run", "^$", "-bench", ".", "-benchmem", "./..."}
	require.Equal(t, len(expected), len(args))
	for i, a := range args {
		assert.Equal(t, expected[i], a)
	}
}

func TestBuildBenchArgsAllOptions(t *testing.T) {
	opts := Options{
		Time:    "5s",
		Count:   3,
		CPU:     "1,2,4",
		Verbose: true,
	}
	args := buildBenchArgs(opts)

	assertContains(t, args, "-bench", ".")
	assertContains(t, args, "-benchmem")
	assertContains(t, args, "-benchtime", "5s")
	assertContains(t, args, "-count", "3")
	assertContains(t, args, "-cpu", "1,2,4")
	assertContains(t, args, "-v")
	assert.Equal(t, "./...", args[len(args)-1])
}

func TestBuildBenchArgsBenchmemAlwaysPresent(t *testing.T) {
	opts := Options{}
	args := buildBenchArgs(opts)

	found := false
	for _, a := range args {
		if a == "-benchmem" {
			found = true
		}
	}
	assert.True(t, found)
}

func TestRunBenchmarksSuccess(t *testing.T) {
	mock := runner.NewMock()
	baseArgs := buildBenchArgs(Options{})
	jsonArgs := append([]string{baseArgs[0], "-json"}, baseArgs[1:]...)
	mock.SetResponse("go", jsonArgs, []byte(`{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}`), nil)

	report, err := RunBenchmarks(mock, Options{})
	assert.Nil(t, err)
	require.NotNil(t, report)
	assert.True(t, report.HasResults())
}

func TestRunBenchmarksFails(t *testing.T) {
	mock := runner.NewMock()
	baseArgs := buildBenchArgs(Options{})
	jsonArgs := append([]string{baseArgs[0], "-json"}, baseArgs[1:]...)
	mock.SetResponse("go", jsonArgs, nil, fmt.Errorf("benchmark failed"))

	report, err := RunBenchmarks(mock, Options{})
	assert.NotNil(t, err)
	assert.Nil(t, report)
}

func TestRunBenchmarksFailsWithPartialResults(t *testing.T) {
	mock := runner.NewMock()
	baseArgs := buildBenchArgs(Options{})
	jsonArgs := append([]string{baseArgs[0], "-json"}, baseArgs[1:]...)
	output := []byte(`{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}`)
	mock.SetResponse("go", jsonArgs, output, fmt.Errorf("benchmark failed"))

	report, err := RunBenchmarks(mock, Options{})
	assert.NotNil(t, err)
	// Should still return partial results
	assert.NotNil(t, report)
}

func TestRunBenchmarksStreamsResults(t *testing.T) {
	mock := runner.NewMock()
	baseArgs := buildBenchArgs(Options{})
	jsonArgs := append([]string{baseArgs[0], "-json"}, baseArgs[1:]...)
	output := []byte(`{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\t  56 B/op\t  3 allocs/op\n"}
{"Action":"output","Package":"pkg","Output":"BenchmarkBar-8   \t 500\t  5678 ns/op\n"}
{"Action":"pass","Package":"pkg"}
`)
	mock.SetResponse("go", jsonArgs, output, nil)

	var streamed bytes.Buffer
	report, err := RunBenchmarks(mock, Options{StreamTo: &streamed})
	assert.Nil(t, err)
	require.NotNil(t, report)
	assert.True(t, report.HasResults())

	got := streamed.String()
	assert.Contains(t, got, "BenchmarkFoo-8")
	assert.Contains(t, got, "1234 ns/op")
	assert.Contains(t, got, "BenchmarkBar-8")
	assert.Contains(t, got, "5678 ns/op")
}

func TestRunBenchmarksNoStreamWhenNil(t *testing.T) {
	mock := runner.NewMock()
	baseArgs := buildBenchArgs(Options{})
	jsonArgs := append([]string{baseArgs[0], "-json"}, baseArgs[1:]...)
	mock.SetResponse("go", jsonArgs, []byte(`{"Action":"output","Package":"pkg","Output":"BenchmarkFoo-8   \t 1000\t  1234 ns/op\n"}`), nil)

	report, err := RunBenchmarks(mock, Options{})
	assert.Nil(t, err)
	require.NotNil(t, report)
	assert.True(t, report.HasResults())
}

func TestHasBenchmarksFindsNone(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/main_test.go", []byte("package main\nfunc TestFoo(t *testing.T) {}\n"), 0644))

	t.Chdir(dir)

	assert.False(t, HasBenchmarks())
}

func TestHasBenchmarksFindsOne(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(dir+"/bench_test.go", []byte("package main\n\nimport \"testing\"\n\nfunc BenchmarkFoo(b *testing.B) {}\n"), 0644))

	t.Chdir(dir)

	assert.True(t, HasBenchmarks())
}

// assertContains checks that args contains the given sequence of values
func assertContains(t *testing.T, args []string, values ...string) {
	t.Helper()
	for i, a := range args {
		if a == values[0] {
			if len(values) == 1 {
				return
			}
			if i+1 < len(args) && args[i+1] == values[1] {
				return
			}
		}
	}
	t.Errorf("args %v does not contain %v", args, values)
}
