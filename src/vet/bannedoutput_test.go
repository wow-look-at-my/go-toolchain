package vet

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/analysis/analysistest"
)

// TestBannedOutputAnalyzer runs the bannedoutput analyzer over its fixture:
// direct fmt/log stdio writes must be reported, while Sprintf-style calls,
// Fprint* to non-stdio writers, and writers held in variables must not.
func TestBannedOutputAnalyzer(t *testing.T) {
	testdata, err := filepath.Abs("testdata")
	require.Nil(t, err)
	analysistest.Run(t, testdata, BannedOutputAnalyzer, "bannedoutput")
}
