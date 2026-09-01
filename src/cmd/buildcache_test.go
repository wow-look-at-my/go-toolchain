package cmd

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cacheEnvVars are the env vars validateCICacheConfig reads.
var cacheEnvVars = []string{
	"CI",
	"GO_BUILDCACHE_CONFIG",
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

func TestValidateCICacheConfig_CIMissingConfig(t *testing.T) {
	defer saveCacheEnv(t)()
	os.Setenv("CI", "true")

	err := validateCICacheConfig()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CI caching not configured")
	assert.Contains(t, err.Error(), "GO_BUILDCACHE_CONFIG")
	assert.Contains(t, err.Error(), "GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED")
}

func TestValidateCICacheConfig_CIBypass(t *testing.T) {
	defer saveCacheEnv(t)()
	os.Setenv("CI", "true")
	os.Setenv("GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED", "1")

	err := validateCICacheConfig()
	assert.NoError(t, err)
}

func TestValidateCICacheConfig_Configured(t *testing.T) {
	defer saveCacheEnv(t)()
	os.Setenv("CI", "true")
	os.Setenv("GO_BUILDCACHE_CONFIG", "eyJlbmRwb2ludCI6InMzLmV4YW1wbGUuY29tIn0=") // {"endpoint":"s3.example.com"}

	err := validateCICacheConfig()
	assert.NoError(t, err)
}
