package cmd

import "testing"

func TestUnpoisonGoVersion(t *testing.T) {
	// A poisoned version is substituted for its known-good replacement.
	if got, poisoned := unpoisonGoVersion("1.24.13"); !poisoned || got != "1.25.0" {
		t.Errorf("unpoisonGoVersion(1.24.13) = %q, %v; want 1.25.0, true", got, poisoned)
	}
	// Clean versions pass through unchanged (no false positives on adjacent patches).
	for _, v := range []string{"1.24.7", "1.24.12", "1.24.14", "1.25.0", "1.23.0"} {
		if got, poisoned := unpoisonGoVersion(v); poisoned || got != v {
			t.Errorf("unpoisonGoVersion(%q) = %q, %v; want %q, false", v, got, poisoned, v)
		}
	}
}
