package logger

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEmitGHAEscaping verifies that annotation message data and the file=
// property are escaped per the GitHub Actions workflow-command encoding:
// data escapes the percent sign, CR and LF; properties additionally escape
// the colon and the comma. Without the data escaping, a multi-line message
// truncates to its leading line in the annotation.
func TestEmitGHAEscaping(t *testing.T) {
	t.Serial()
	cases := []struct {
		name string
		emit func(w *strings.Builder, file, msg string)
		file string
		msg  string
		want string
	}{
		{
			name: "warning plain single line",
			emit: func(w *strings.Builder, file, msg string) { EmitGHAWarning(w, file, msg) },
			msg:  "simple message",
			want: "::warning ::simple message\n",
		},
		{
			name: "warning multi-line message",
			emit: func(w *strings.Builder, file, msg string) { EmitGHAWarning(w, file, msg) },
			msg:  "first line\nsecond line\nthird line",
			want: "::warning ::first line%0Asecond line%0Athird line\n",
		},
		{
			name: "error multi-line message",
			emit: func(w *strings.Builder, file, msg string) { EmitGHAError(w, file, msg) },
			msg:  "go generate failed:\nexit status 1",
			want: "::error ::go generate failed:%0Aexit status 1\n",
		},
		{
			name: "percent in message",
			emit: func(w *strings.Builder, file, msg string) { EmitGHAWarning(w, file, msg) },
			msg:  "coverage dropped to 82.7%",
			want: "::warning ::coverage dropped to 82.7%25\n",
		},
		{
			name: "percent escaped before newline (no double escape)",
			emit: func(w *strings.Builder, file, msg string) { EmitGHAWarning(w, file, msg) },
			msg:  "literal %0A stays literal\nreal newline",
			want: "::warning ::literal %250A stays literal%0Areal newline\n",
		},
		{
			name: "carriage return in message",
			emit: func(w *strings.Builder, file, msg string) { EmitGHAError(w, file, msg) },
			msg:  "windows\r\nline",
			want: "::error ::windows%0D%0Aline\n",
		},
		{
			name: "colon and comma NOT escaped in message data",
			emit: func(w *strings.Builder, file, msg string) { EmitGHAWarning(w, file, msg) },
			msg:  "key: a, b",
			want: "::warning ::key: a, b\n",
		},
		{
			name: "file property with colon and comma",
			emit: func(w *strings.Builder, file, msg string) { EmitGHAWarning(w, file, msg) },
			file: "dir:sub,file.go",
			msg:  "msg",
			want: "::warning file=dir%3Asub%2Cfile.go::msg\n",
		},
		{
			name: "file property with percent and newline",
			emit: func(w *strings.Builder, file, msg string) { EmitGHAError(w, file, msg) },
			file: "odd%name\n.go",
			msg:  "msg",
			want: "::error file=odd%25name%0A.go::msg\n",
		},
		{
			name: "plain file property unchanged",
			emit: func(w *strings.Builder, file, msg string) { EmitGHAError(w, file, msg) },
			file: "src/cmd/root.go",
			msg:  "boom",
			want: "::error file=src/cmd/root.go::boom\n",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var sb strings.Builder
			c.emit(&sb, c.file, c.msg)
			assert.Equal(t, c.want, sb.String())
		})
	}
}

// TestLoggerGHAEscaping verifies escaping end-to-end through the Logger API
// (Warn/Error/WarnFile/ErrorFile in GHA mode).
func TestLoggerGHAEscaping(t *testing.T) {
	t.Serial()
	l, out, errBuf := captureLogger(LevelDebug, true)

	l.Warn("line one\nline two")
	assert.Contains(t, out.String(), "::warning ::line one%0Aline two\n")
	assert.Equal(t, 0, errBuf.Len())

	out.Reset()
	l.Error("100%% is %d%%", 100)
	assert.Contains(t, out.String(), "::error ::100%25 is 100%25\n")

	out.Reset()
	l.WarnFile("a:b.go", "multi\nline")
	assert.Contains(t, out.String(), "::warning file=a%3Ab.go::multi%0Aline\n")

	out.Reset()
	l.ErrorFile("c,d.go", "oops")
	assert.Contains(t, out.String(), "::error file=c%2Cd.go::oops\n")
}

// TestNonGHAOutputUnescaped verifies that outside GHA mode the plain stderr
// output is NOT escaped: multi-line messages and percent signs pass through
// byte-identically.
func TestNonGHAOutputUnescaped(t *testing.T) {
	t.Serial()
	l, out, errBuf := captureLogger(LevelDebug, false)

	l.Warn("line one\nline two with 50%% done")
	assert.Equal(t, 0, out.Len())
	got := errBuf.String()
	assert.Contains(t, got, "line one\nline two with 50% done")
	assert.NotContains(t, got, "%0A")
	assert.NotContains(t, got, "%25")

	errBuf.Reset()
	l.Error("err: a\nb")
	got = errBuf.String()
	assert.Contains(t, got, "err: a\nb")
	assert.NotContains(t, got, "%0A")
}
