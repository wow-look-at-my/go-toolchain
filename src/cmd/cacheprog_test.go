package cmd

import (
	"os"
	"testing"

	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
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

	err := enableCacheProg()
	require.NoError(t, err)

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
	assert.Equal(t, "==> Cache: disabled\n", output)
}

// cacheEnvVars are the env vars required for CI caching.
var cacheEnvVars = []string{
	"CI",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"GOCACHE_S3_ENDPOINT",
	"GOCACHE_S3_REGION",
	"GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED",
}

// saveCacheEnv saves and clears all cache-related env vars, returning a restore function.
func saveCacheEnv(t *testing.T) func() {
	t.Helper()
	saved := make(map[string]string)
	for _, k := range cacheEnvVars {
		saved[k] = os.Getenv(k)
		os.Unsetenv(k)
	}
	return func() {
		for _, k := range cacheEnvVars {
			if v, ok := saved[k]; ok && v != "" {
				os.Setenv(k, v)
			} else {
				os.Unsetenv(k)
			}
		}
	}
}

func TestValidateCICacheConfig_NotCI(t *testing.T) {
	defer saveCacheEnv(t)()
	// No CI env var — should pass even with no cache vars
	err := validateCICacheConfig()
	assert.NoError(t, err)
}

func TestValidateCICacheConfig_CIMissingVars(t *testing.T) {
	defer saveCacheEnv(t)()
	os.Setenv("CI", "true")

	err := validateCICacheConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CI caching not configured")
	assert.Contains(t, err.Error(), "AWS_ACCESS_KEY_ID")
	assert.Contains(t, err.Error(), "AWS_SECRET_ACCESS_KEY")
	assert.Contains(t, err.Error(), "GOCACHE_S3_ENDPOINT")
	assert.Contains(t, err.Error(), "GOCACHE_S3_REGION")
	assert.Contains(t, err.Error(), "GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED")
}

func TestValidateCICacheConfig_CIPartialVars(t *testing.T) {
	defer saveCacheEnv(t)()
	os.Setenv("CI", "true")
	os.Setenv("AWS_ACCESS_KEY_ID", "key")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "secret")

	err := validateCICacheConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "GOCACHE_S3_ENDPOINT")
	assert.Contains(t, err.Error(), "GOCACHE_S3_REGION")
	assert.NotContains(t, err.Error(), "AWS_ACCESS_KEY_ID")
}

func TestValidateCICacheConfig_CIAllVarsSet(t *testing.T) {
	defer saveCacheEnv(t)()
	os.Setenv("CI", "true")
	os.Setenv("AWS_ACCESS_KEY_ID", "key")
	os.Setenv("AWS_SECRET_ACCESS_KEY", "secret")
	os.Setenv("GOCACHE_S3_ENDPOINT", "https://s3.example.com")
	os.Setenv("GOCACHE_S3_REGION", "us-east-1")

	err := validateCICacheConfig()
	assert.NoError(t, err)
}

func TestValidateCICacheConfig_CIBypass(t *testing.T) {
	defer saveCacheEnv(t)()
	os.Setenv("CI", "true")
	os.Setenv("GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED", "1")

	err := validateCICacheConfig()
	assert.NoError(t, err)
}
