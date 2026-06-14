package cmd

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/wow-look-at-my/go-toolchain/src/cache"
)

func TestLooksLikeCachePoison(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want bool
	}{
		{
			name: "export data cross-contamination (real observed failure)",
			msg:  "vet failed: package load errors:\nload_test.go:113:10: undefined: runtime\nload_test.go:13:2: \"runtime\" imported as reflectlite and not used",
			want: true,
		},
		{
			name: "module index for std package",
			msg:  "package runtime is not in std",
			want: true,
		},
		{
			name: "corrupt module index",
			msg:  "loading module index: corrupt index",
			want: true,
		},
		{
			name: "legitimate unused aliased import is NOT poison (no undefined)",
			msg:  "vet failed: package load errors:\nmain.go:3:8: \"fmt\" imported as f and not used",
			want: false,
		},
		{
			name: "ordinary undefined symbol is NOT poison",
			msg:  "build failed: ./main.go:10:2: undefined: doesNotExist",
			want: false,
		},
		{
			name: "generic build failure",
			msg:  "exit status 1",
			want: false,
		},
		{
			name: "empty",
			msg:  "",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, looksLikeCachePoison(tc.msg))
		})
	}
}

func TestInCacheRecovery(t *testing.T) {
	t.Setenv(cacheRecoveryEnv, "")
	assert.False(t, inCacheRecovery())
	t.Setenv(cacheRecoveryEnv, "1")
	assert.True(t, inCacheRecovery())
}

func TestShouldRetryForCachePoison(t *testing.T) {
	poison := errors.New("vet failed: package load errors:\nx.go:1:2: undefined: runtime\nx.go:1:2: \"runtime\" imported as reflectlite and not used")
	plain := errors.New("exit status 1")

	t.Run("poison error outside recovery retries", func(t *testing.T) {
		t.Setenv(cacheRecoveryEnv, "")
		assert.True(t, ShouldRetryForCachePoison(poison))
	})
	t.Run("poison error during recovery does NOT retry again", func(t *testing.T) {
		t.Setenv(cacheRecoveryEnv, "1")
		assert.False(t, ShouldRetryForCachePoison(poison))
	})
	t.Run("non-poison error never retries", func(t *testing.T) {
		t.Setenv(cacheRecoveryEnv, "")
		assert.False(t, ShouldRetryForCachePoison(plain))
	})
	t.Run("nil error never retries", func(t *testing.T) {
		t.Setenv(cacheRecoveryEnv, "")
		assert.False(t, ShouldRetryForCachePoison(nil))
	})
}

func TestRecoveryCommand(t *testing.T) {
	t.Setenv(cacheRecoveryEnv, "")
	c, err := recoveryCommand()
	require.NoError(t, err)

	// Same executable and forwarded args as this process.
	exe, _ := os.Executable()
	assert.Equal(t, exe, c.Path)
	assert.Equal(t, os.Args[1:], c.Args[1:])

	// The child is marked as the recovery retry exactly once.
	var n int
	for _, kv := range c.Env {
		if kv == cacheRecoveryEnv+"=1" {
			n++
		}
	}
	assert.Equal(t, 1, n, "child env must request cache recovery exactly once")
	assert.Same(t, os.Stderr, c.Stderr)
}

func TestBuildCacheDir(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", "/tmp/xdgcache")

	t.Setenv(cacheRecoveryEnv, "")
	assert.Equal(t, filepath.Join("/tmp/xdgcache", "go-toolchain", "buildcache"), buildCacheDir())

	t.Setenv(cacheRecoveryEnv, "1")
	assert.Equal(t, filepath.Join("/tmp/xdgcache", "go-toolchain", "buildcache-recovery"), buildCacheDir())
}

// During recovery the remote cache is disabled unconditionally, even when a
// valid GO_BUILDCACHE_CONFIG is present — so a poisoned shared cache cannot
// re-break the retry.
func TestParseBuildCacheConfig_RecoveryDisablesRemote(t *testing.T) {
	defer saveCacheEnv(t)()

	raw := `{"endpoint":"cache.example.com","bucket":"b","username":"u","password":"p"}`
	os.Setenv("GO_BUILDCACHE_CONFIG", base64.StdEncoding.EncodeToString([]byte(raw)))
	t.Setenv(cacheRecoveryEnv, "1")

	cfg := parseBuildCacheConfig()
	assert.Equal(t, cache.WebConfig{}, cfg, "recovery must disable the remote cache")

	// Sanity: the same config IS honored when not in recovery.
	t.Setenv(cacheRecoveryEnv, "")
	cfg = parseBuildCacheConfig()
	assert.Equal(t, "cache.example.com", cfg.Endpoint)
	assert.Equal(t, "u", cfg.AccessKey)
}
