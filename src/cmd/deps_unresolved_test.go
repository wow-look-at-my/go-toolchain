package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// A dependency the proxy cannot answer for used to be skipped in silence. For
// an org dependency that silence hides a pin nobody is maintaining: those are
// the ones autoUpdateDeps repoints, so a skipped check is the difference
// between a pin that follows its branch and one frozen on a commit git may no
// longer reach.

// missCache keeps every check live: a cached verdict would answer before the
// proxy is ever asked, which is the path under test.
type missCache struct{}

func (missCache) lookup(string, string) (string, int64, bool) { return "", 0, false }
func (missCache) store(string, string, string, int64)         {}
func (missCache) close()                                      {}

// deadProxyChecker points GOPROXY at a server that answers every query with
// 500, then runs the checker over goMod in a scratch directory.
func deadProxyChecker(t *testing.T, goMod string) {
	t.Helper()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(proxy.Close)
	t.Setenv("GOPROXY", proxy.URL)

	dir := t.TempDir()
	orig, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() { os.Chdir(orig) })
	require.NoError(t, os.WriteFile("go.mod", []byte(goMod), 0644))

	logger.ResetWarnCount()
	dc := &DepChecker{cache: missCache{}, doneCh: make(chan struct{})}
	dc.run()
}

func TestCheckAll_WarnsWhenAnOrgPinCannotBeChecked(t *testing.T) {
	deadProxyChecker(t, `module github.com/wow-look-at-my/consumer

go 1.21

require github.com/wow-look-at-my/dep v0.0.0-20240101120000-abc123def456
`)
	assert.Positive(t, logger.WarnCount(), "an uncheckable org pin must not pass in silence")
}

// The warning is scoped to the org prefix on purpose. A third-party module the
// proxy is briefly unhappy about is not a pin this toolchain maintains, and
// warning about it would spend the warnings budget on noise.
func TestCheckAll_QuietWhenAThirdPartyPinCannotBeChecked(t *testing.T) {
	deadProxyChecker(t, `module github.com/wow-look-at-my/consumer

go 1.21

require github.com/other/dep v0.0.0-20240101120000-abc123def456
`)
	assert.Zero(t, logger.WarnCount(), "a third-party pin is not ours to maintain")
}

// A branch-tracked require never reaches the proxy check at all -- depsbranch
// owns it -- so it cannot produce this warning either.
func TestCheckAll_QuietForABranchTrackedPin(t *testing.T) {
	deadProxyChecker(t, `module github.com/wow-look-at-my/consumer

go 1.21

require github.com/wow-look-at-my/dep v0.0.0-20240101120000-abc123def456 // go-toolchain:branch=master
`)
	assert.Zero(t, logger.WarnCount(), "depsbranch owns a tracked pin, not the proxy check")
}
