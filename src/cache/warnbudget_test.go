package cache

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCacheWarningsAreNotBudgeted pins the rule that every warning emitted by
// this package goes through logger.WarnInfra, never logger.Warn/WarnFile.
//
// The cache tier is infrastructure: its warnings describe the machine, the
// disk and the shared cache server, never the source tree being built. A
// project cannot fix "index fetch: context deadline exceeded" by editing its
// own code, and whether that warning fires at all depends on network weather.
// Counting such messages against the pipeline's warnings budget (see
// src/cmd/warningsgate.go) made the gate nondeterministic: the same commit
// failed or passed depending on how fast the cache server answered that
// minute. A plain logger.Warn added here would silently restore that, so the
// rule is enforced mechanically rather than by review.
func TestCacheWarningsAreNotBudgeted(t *testing.T) {
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	banned := []string{"logger.Warn(", "logger.WarnFile("}
	var offenders []string
	var scanned int

	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", name))
		require.NoError(t, err)
		scanned++
		for i, line := range strings.Split(string(src), "\n") {
			for _, b := range banned {
				if strings.Contains(line, b) {
					offenders = append(offenders, filepath.Join("src/cache", name)+":"+strconv.Itoa(i+1)+": "+strings.TrimSpace(line))
				}
			}
		}
	}

	require.NotZero(t, scanned, "found no non-test .go files to scan — the guard would pass vacuously")
	require.Empty(t, offenders, "src/cache must emit warnings via logger.WarnInfra (infrastructure warnings do not "+
		"count against the build's warnings budget); offending call sites:\n%s", strings.Join(offenders, "\n"))
}
