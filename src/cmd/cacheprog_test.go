package cmd

import (
	"encoding/base64"
	"fmt"
	"os"
	"testing"

	"github.com/wow-look-at-my/go-toolchain/src/cache"
	"github.com/wow-look-at-my/testify/assert"
	"github.com/wow-look-at-my/testify/require"
)

func TestEnableCacheProg(t *testing.T) {
	origProg := os.Getenv("GOCACHEPROG")
	origSock := os.Getenv("GOCACHE_STATS_SOCK")
	origListener := statsListener
	origMinor := resolvedGoMinor
	defer func() {
		os.Setenv("GOCACHEPROG", origProg)
		os.Setenv("GOCACHE_STATS_SOCK", origSock)
		if statsListener != nil && statsListener != origListener {
			statsListener.Close()
		}
		statsListener = origListener
		resolvedGoMinor = origMinor
	}()

	os.Unsetenv("GOCACHEPROG")
	os.Unsetenv("GOCACHE_STATS_SOCK")
	statsListener = nil
	resolvedGoMinor = 25 // simulate bootstrapped Go 1.25

	err := enableCacheProg()
	require.NoError(t, err)

	prog := os.Getenv("GOCACHEPROG")
	assert.Contains(t, prog, "cacheprog")

	sockPath := os.Getenv("GOCACHE_STATS_SOCK")
	assert.NotEmpty(t, sockPath)
	assert.NotNil(t, statsListener)
}

func TestGoSupportsFeature_CacheProg(t *testing.T) {
	old := resolvedGoMinor
	defer func() { resolvedGoMinor = old }()
	resolvedGoMinor = 24
	assert.True(t, goSupportsFeature(FeatureCacheProg))
}

func TestGoSupportsFeature_FutureVersion(t *testing.T) {
	old := resolvedGoMinor
	defer func() { resolvedGoMinor = old }()
	resolvedGoMinor = 24
	future := GoFeature{Name: "future", MinorVersion: 99}
	assert.False(t, goSupportsFeature(future))
}

func TestPrintCacheStats_NoListener(t *testing.T) {
	old := statsListener
	oldEnabled := cacheEnabled
	oldErr := cacheSetupErr
	oldMinor := resolvedGoMinor
	statsListener = nil
	cacheEnabled = true
	cacheSetupErr = fmt.Errorf("stats listener: dial unix /tmp/x.sock: permission denied")
	resolvedGoMinor = 25 // simulate bootstrapped Go 1.25
	defer func() {
		statsListener = old
		cacheEnabled = oldEnabled
		cacheSetupErr = oldErr
		resolvedGoMinor = oldMinor
	}()

	output := captureStdout(func() {
		printCacheStats(true)
	})
	assert.Equal(t, "==> Cache: disabled (stats listener: dial unix /tmp/x.sock: permission denied)\n", output)
}

func TestPrintCacheStats_NoCacheCommand(t *testing.T) {
	old := statsListener
	oldEnabled := cacheEnabled
	statsListener = nil
	cacheEnabled = false
	defer func() {
		statsListener = old
		cacheEnabled = oldEnabled
	}()

	output := captureStdout(func() {
		printCacheStats(true)
	})
	assert.Equal(t, "", output)
}

// cacheEnvVars are the env vars required for CI caching.
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

func TestValidateCICacheConfig_UnifiedVar(t *testing.T) {
	defer saveCacheEnv(t)()
	os.Setenv("CI", "true")
	os.Setenv("GO_BUILDCACHE_CONFIG", "eyJlbmRwb2ludCI6InMzLmV4YW1wbGUuY29tIn0=") // {"endpoint":"s3.example.com"}

	err := validateCICacheConfig()
	assert.NoError(t, err)
}

func TestParseBuildCacheConfig_Unified(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"s3.example.com","bucket":"mybucket","region":"eu-west-1","key_id":"AKID","access_key":"SECRET"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.S3Config{
		Endpoint:  "s3.example.com",
		Bucket:    "mybucket",
		Region:    "eu-west-1",
		AccessKey: "AKID",
		SecretKey: "SECRET",
		Version:   buildVersion,
	}, cfg)
}

func TestParseBuildCacheConfig_UnifiedDefaultBucket(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"s3.example.com","region":"us-east-1","key_id":"AKID","access_key":"SECRET"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, "gobuildcache", cfg.Bucket)
}

func TestParseBuildCacheConfig_NotSet(t *testing.T) {
	defer saveCacheEnv(t)()

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.S3Config{}, cfg)
}

func TestParseBuildCacheConfig_URLSafeBase64(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"s3.example.com","bucket":"mybucket","region":"eu-west-1","key_id":"AKID","access_key":"SECRET"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.URLEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, "s3.example.com", cfg.Endpoint)
	assert.Equal(t, "mybucket", cfg.Bucket)
}

func TestParseBuildCacheConfig_RawBase64(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"s3.example.com","bucket":"mybucket","region":"eu-west-1","key_id":"AKID","access_key":"SECRET"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.RawStdEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, "s3.example.com", cfg.Endpoint)
	assert.Equal(t, "mybucket", cfg.Bucket)
}

func TestParseBuildCacheConfig_MissingEndpoint(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"bucket":"mybucket","key_id":"AKID","access_key":"SECRET"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.S3Config{}, cfg)
}

func TestParseBuildCacheConfig_MissingKeys(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"s3.example.com","bucket":"mybucket"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.S3Config{}, cfg)
}

func TestParseBuildCacheConfig_BadBase64(t *testing.T) {
	defer saveCacheEnv(t)()

	os.Setenv("GO_BUILDCACHE_CONFIG", "not-valid-base64!!!")

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.S3Config{}, cfg)
}

func TestParseBuildCacheConfig_BadJSON(t *testing.T) {
	defer saveCacheEnv(t)()

	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte("not json")))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.S3Config{}, cfg)
}
