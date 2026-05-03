package codeql

import (
	"errors"
	"testing"

	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func TestEnabled(t *testing.T) {
	t.Setenv("CODEQL_DIST", "")
	if Enabled() {
		t.Fatal("Enabled() = true with CODEQL_DIST unset")
	}
	t.Setenv("CODEQL_DIST", "/opt/codeql")
	if !Enabled() {
		t.Fatal("Enabled() = false with CODEQL_DIST set")
	}
}

func TestExtractInvokesGoExtractor(t *testing.T) {
	t.Setenv("CODEQL_EXTRACTOR_GO_ROOT", "/opt/codeql/go")
	mock := runner.NewMock()
	if err := Extract(mock); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	want := "/opt/codeql/go/tools/linux64/go-extractor"
	if calls[0].Name != want && calls[0].Name != "/opt/codeql/go/tools/osx64/go-extractor" && calls[0].Name != "/opt/codeql/go/tools/win64/go-extractor.exe" {
		t.Errorf("extractor path = %q, want platform-suffixed go-extractor under %s", calls[0].Name, want)
	}
	if len(calls[0].Args) != 1 || calls[0].Args[0] != "./..." {
		t.Errorf("args = %v, want [./...]", calls[0].Args)
	}
}

func TestExtractMissingEnv(t *testing.T) {
	t.Setenv("CODEQL_EXTRACTOR_GO_ROOT", "")
	mock := runner.NewMock()
	if err := Extract(mock); err == nil {
		t.Fatal("Extract: want error when CODEQL_EXTRACTOR_GO_ROOT unset")
	}
}

func TestExtractPropagatesStderrOnFailure(t *testing.T) {
	t.Setenv("CODEQL_EXTRACTOR_GO_ROOT", "/opt/codeql/go")
	mock := runner.NewMock()
	mock.Handler = func(cfg runner.Config) (runner.IProcess, error) {
		return runner.MockProcessWithStderr(nil, []byte("permission denied"), errors.New("exit 1")), nil
	}
	err := Extract(mock)
	if err == nil {
		t.Fatal("Extract: want error")
	}
	if !contains(err.Error(), "permission denied") {
		t.Errorf("error %q does not contain stderr", err)
	}
}

func TestAnalyzeRunsFinalizeAndAnalyze(t *testing.T) {
	t.Setenv("CODEQL_DIST", "/opt/codeql")
	t.Setenv("CODEQL_EXTRACTOR_GO_WIP_DATABASE", "/tmp/db")
	mock := runner.NewMock()
	sarif, err := Analyze(mock)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if sarif == "" {
		t.Fatal("Analyze: empty sarif path")
	}
	calls := mock.Calls()
	if len(calls) != 2 {
		t.Fatalf("want 2 calls, got %d", len(calls))
	}
	if calls[0].Args[0] != "database" || calls[0].Args[1] != "finalize" {
		t.Errorf("first call = %v, want codeql database finalize", calls[0].Args)
	}
	if calls[1].Args[0] != "database" || calls[1].Args[1] != "analyze" {
		t.Errorf("second call = %v, want codeql database analyze", calls[1].Args)
	}
}

func TestAnalyzeMissingDatabase(t *testing.T) {
	t.Setenv("CODEQL_EXTRACTOR_GO_WIP_DATABASE", "")
	if _, err := Analyze(runner.NewMock()); err == nil {
		t.Fatal("Analyze: want error when CODEQL_EXTRACTOR_GO_WIP_DATABASE unset")
	}
}

func TestUploadSARIFRequiresEnv(t *testing.T) {
	cases := []struct {
		name           string
		token, sha, ref, repo string
	}{
		{"no-token", "", "abc", "refs/heads/main", "o/r"},
		{"no-sha", "tok", "", "refs/heads/main", "o/r"},
		{"no-ref", "tok", "abc", "", "o/r"},
		{"no-repo", "tok", "abc", "refs/heads/main", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("GITHUB_TOKEN", c.token)
			t.Setenv("GH_TOKEN", "")
			t.Setenv("GITHUB_SHA", c.sha)
			t.Setenv("GITHUB_REF", c.ref)
			t.Setenv("GITHUB_REPOSITORY", c.repo)
			t.Setenv("CODEQL_DIST", "/opt/codeql")
			if err := UploadSARIF(runner.NewMock(), "/tmp/r.sarif"); err == nil {
				t.Fatal("UploadSARIF: want error when required env missing")
			}
		})
	}
}

func TestUploadSARIFPassesAllArgs(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "tok")
	t.Setenv("GITHUB_SHA", "deadbeef")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("GITHUB_REPOSITORY", "wow-look-at-my/go-toolchain")
	t.Setenv("CODEQL_DIST", "/opt/codeql")
	mock := runner.NewMock()
	if err := UploadSARIF(mock, "/tmp/r.sarif"); err != nil {
		t.Fatalf("UploadSARIF: %v", err)
	}
	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	want := []string{"github", "upload-results",
		"--sarif=/tmp/r.sarif",
		"--commit=deadbeef",
		"--ref=refs/heads/main",
		"--repository=wow-look-at-my/go-toolchain",
	}
	if len(calls[0].Args) != len(want) {
		t.Fatalf("args length = %d, want %d (%v)", len(calls[0].Args), len(want), calls[0].Args)
	}
	for i, a := range want {
		if calls[0].Args[i] != a {
			t.Errorf("arg[%d] = %q, want %q", i, calls[0].Args[i], a)
		}
	}
}

func TestUploadSARIFFallsBackToGHToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "ghtok")
	t.Setenv("GITHUB_SHA", "deadbeef")
	t.Setenv("GITHUB_REF", "refs/heads/main")
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	t.Setenv("CODEQL_DIST", "/opt/codeql")
	mock := runner.NewMock()
	if err := UploadSARIF(mock, "/tmp/r.sarif"); err != nil {
		t.Fatalf("UploadSARIF: %v", err)
	}
	calls := mock.Calls()
	if len(calls) != 1 {
		t.Fatalf("want 1 call, got %d", len(calls))
	}
	if got, _ := calls[0].Env.Get("GITHUB_TOKEN"); got != "ghtok" {
		t.Errorf("GITHUB_TOKEN env override = %q, want %q", got, "ghtok")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
