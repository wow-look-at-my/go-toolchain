package cmd

import (
	"encoding/base64"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-toolchain/src/cache"
)

func TestEnableCacheProg(t *testing.T) {
	// Without XDG_CACHE_HOME, enableCacheProg would mount the developer's real cache and scan every pack -- slow, and not this test's concern.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	origProg := os.Getenv("GOCACHEPROG")
	origSock := os.Getenv("GOCACHE_STATS_SOCK")
	origDaemonSock := os.Getenv("GOCACHE_DAEMON_SOCK")
	origListener := statsListener
	origDaemon := cacheDaemon
	origMinor := resolvedGoMinor
	defer func() {
		os.Setenv("GOCACHEPROG", origProg)
		os.Setenv("GOCACHE_STATS_SOCK", origSock)
		os.Setenv("GOCACHE_DAEMON_SOCK", origDaemonSock)
		// Close daemon BEFORE stats listener — the daemon holds a stats
		// socket connection that the listener waits on during Close().
		if cacheDaemon != nil && cacheDaemon != origDaemon {
			cacheDaemon.Close()
		}
		cacheDaemon = origDaemon
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
	assert.Equal(t, "⇒ Cache: disabled (stats listener: dial unix /tmp/x.sock: permission denied)\n", output)
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

	raw := `{"endpoint":"s3.example.com","bucket":"mybucket","key_id":"AKID","access_key":"SECRET"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.WebConfig{
		Endpoint:  "s3.example.com",
		Bucket:    "mybucket",
		AccessKey: "AKID",
		SecretKey: "SECRET",
		Version:   buildVersion,
	}, cfg)
}

// TestParseBuildCacheConfig_NativeCredentials exercises the native, non-S3
// credential field names (username/password, mapped onto WebConfig's Basic Auth
// AccessKey/SecretKey).
func TestParseBuildCacheConfig_NativeCredentials(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"cache.example.com","bucket":"mybucket","username":"alice","password":"hunter2"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.WebConfig{
		Endpoint:  "cache.example.com",
		Bucket:    "mybucket",
		AccessKey: "alice",
		SecretKey: "hunter2",
		Version:   buildVersion,
	}, cfg)
}

// TestParseBuildCacheConfig_NativeOverridesDeprecated verifies the native fields
// win when both native and deprecated S3-style fields are present.
func TestParseBuildCacheConfig_NativeOverridesDeprecated(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"cache.example.com","username":"alice","password":"hunter2","key_id":"AKID","access_key":"SECRET"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, "alice", cfg.AccessKey)
	assert.Equal(t, "hunter2", cfg.SecretKey)
}

func TestParseBuildCacheConfig_UnifiedDefaultBucket(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"s3.example.com","key_id":"AKID","access_key":"SECRET"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, "gobuildcache", cfg.Bucket)
}

func TestParseBuildCacheConfig_NotSet(t *testing.T) {
	defer saveCacheEnv(t)()

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.WebConfig{}, cfg)
}

func TestParseBuildCacheConfig_URLSafeBase64(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"s3.example.com","bucket":"mybucket","region":"eu-west-1","key_id":"AKID","access_key":"SECRET"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.URLEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, "s3.example.com", cfg.Endpoint)
	assert.Equal(t, "mybucket", cfg.Bucket)
}

func TestParseBuildCacheConfig_LineWrappedBase64(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"s3.example.com","bucket":"mybucket","region":"eu-west-1","key_id":"AKID","access_key":"SECRET"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	// Insert newlines every 76 characters (MIME-style wrapping)
	var wrapped string
	for i := 0; i < len(encoded); i += 76 {
		end := i + 76
		if end > len(encoded) {
			end = len(encoded)
		}
		wrapped += encoded[i:end] + "\n"
	}
	os.Setenv("GO_BUILDCACHE_CONFIG", wrapped)

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
	assert.Equal(t, cache.WebConfig{}, cfg)
}

func TestParseBuildCacheConfig_MissingKeys(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"s3.example.com","bucket":"mybucket"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.WebConfig{}, cfg)
}

func TestParseBuildCacheConfig_BadBase64(t *testing.T) {
	defer saveCacheEnv(t)()

	os.Setenv("GO_BUILDCACHE_CONFIG", "not-valid-base64!!!")

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.WebConfig{}, cfg)
}

func TestParseBuildCacheConfig_BadJSON(t *testing.T) {
	defer saveCacheEnv(t)()

	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte("not json")))

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.WebConfig{}, cfg)
}

// TestDaemonSockUnlessNamespaced: a cacheprog running under a cache key
// namespace (a fork-toolchain build) must NEVER proxy to the shared daemon —
// the proxy is a raw byte pipe and the daemon serves unnamespaced clients, so
// proxying would silently drop the namespace and reopen cross-toolchain cache
// poisoning. Unnamespaced cacheprogs keep the daemon fast path.
func TestDaemonSockUnlessNamespaced(t *testing.T) {
	t.Setenv("GOCACHE_DAEMON_SOCK", "/tmp/some-daemon.sock")
	assert.Equal(t, "/tmp/some-daemon.sock", daemonSockUnlessNamespaced(""),
		"unnamespaced cacheprogs must keep proxying to the daemon")
	assert.Equal(t, "", daemonSockUnlessNamespaced("deadbeef00c0ffee"),
		"a namespaced cacheprog must never proxy to the shared daemon")

	t.Setenv("GOCACHE_DAEMON_SOCK", "")
	assert.Equal(t, "", daemonSockUnlessNamespaced(""))
	assert.Equal(t, "", daemonSockUnlessNamespaced("deadbeef00c0ffee"))
}
