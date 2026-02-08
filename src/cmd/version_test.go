package cmd

import (
	"strings"
	"testing"
	"time"
)

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "1 minute"},
		{2 * time.Minute, "2 minutes"},
		{59 * time.Minute, "59 minutes"},
		{1 * time.Hour, "1 hour"},
		{3 * time.Hour, "3 hours"},
		{23 * time.Hour, "23 hours"},
		{24 * time.Hour, "1 day"},
		{48 * time.Hour, "2 days"},
		{72 * time.Hour, "3 days"},
	}
	for _, tt := range tests {
		got := formatDuration(tt.d)
		if got != tt.want {
			t.Errorf("formatDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestCollectGitInfo(t *testing.T) {
	info := collectGitInfo()
	// We're in a git repo, so all fields should be populated
	if info.commit == "" {
		t.Error("expected non-empty commit")
	}
	if info.timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
	// version comes from git describe, should always work with --always
	if info.version == "" {
		t.Error("expected non-empty version")
	}
}

func TestGitInfoLdflags(t *testing.T) {
	info := collectGitInfo()
	ldflags := info.ldflags()

	if ldflags == "" {
		t.Error("expected non-empty ldflags")
	}
	if !strings.Contains(ldflags, "buildVersion") {
		t.Error("expected ldflags to contain buildVersion")
	}
	if !strings.Contains(ldflags, "buildCommit") {
		t.Error("expected ldflags to contain buildCommit")
	}
	if !strings.Contains(ldflags, "buildTimestamp") {
		t.Error("expected ldflags to contain buildTimestamp")
	}
	if !strings.Contains(ldflags, "buildDate") {
		t.Error("expected ldflags to contain buildDate")
	}
}

func TestGitInfoString(t *testing.T) {
	// With version set
	info := gitInfo{version: "v1.0.0", commit: "abc1234567890"}
	if s := info.String(); s != "v1.0.0" {
		t.Errorf("expected 'v1.0.0', got %q", s)
	}

	// Without version, with long commit
	info = gitInfo{commit: "abc1234567890"}
	if s := info.String(); s != "abc1234" {
		t.Errorf("expected 'abc1234', got %q", s)
	}

	// Without version, with short commit
	info = gitInfo{commit: "abc"}
	if s := info.String(); s != "abc" {
		t.Errorf("expected 'abc', got %q", s)
	}

	// Empty
	info = gitInfo{}
	if s := info.String(); s != "unknown" {
		t.Errorf("expected 'unknown', got %q", s)
	}
}

func TestPrintVersionInfo(t *testing.T) {
	// Just verify it doesn't panic with defaults
	old := buildVersion
	defer func() { buildVersion = old }()
	buildVersion = "test-version"
	printVersionInfo()
}

func TestPrintStalenessNoTimestamp(t *testing.T) {
	old := buildTimestamp
	defer func() { buildTimestamp = old }()
	buildTimestamp = ""
	// Should return early without panicking
	printStaleness()
}

func TestPrintStalenessNoCommit(t *testing.T) {
	oldTs := buildTimestamp
	oldCommit := buildCommit
	defer func() {
		buildTimestamp = oldTs
		buildCommit = oldCommit
	}()
	buildTimestamp = "1234567890"
	buildCommit = "unknown"
	// Should return early without panicking
	printStaleness()
}
