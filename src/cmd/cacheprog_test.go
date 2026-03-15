package cmd

import (
	"os"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
)

func TestEnableCacheProg(t *testing.T) {
	origProg := os.Getenv("GOCACHEPROG")
	origSock := os.Getenv("GOCACHE_STATS_SOCK")
	origListener := statsListener
	defer func() {
		os.Setenv("GOCACHEPROG", origProg)
		os.Setenv("GOCACHE_STATS_SOCK", origSock)
		if statsListener != nil && statsListener != origListener {
			statsListener.Close()
		}
		statsListener = origListener
	}()

	os.Unsetenv("GOCACHEPROG")
	os.Unsetenv("GOCACHE_STATS_SOCK")
	statsListener = nil

	enableCacheProg()

	prog := os.Getenv("GOCACHEPROG")
	assert.Contains(t, prog, "cacheprog")

	sockPath := os.Getenv("GOCACHE_STATS_SOCK")
	assert.NotEmpty(t, sockPath)
	assert.NotNil(t, statsListener)
}

func TestGoSupportsFeature_CacheProg(t *testing.T) {
	assert.True(t, goSupportsFeature(FeatureCacheProg))
}

func TestGoSupportsFeature_FutureVersion(t *testing.T) {
	future := GoFeature{Name: "future", MinorVersion: 99}
	assert.False(t, goSupportsFeature(future))
}

func TestPrintCacheStats_NoListener(t *testing.T) {
	old := statsListener
	statsListener = nil
	defer func() { statsListener = old }()

	output := captureStdout(func() {
		printCacheStats()
	})
	assert.Equal(t, "", output)
}
