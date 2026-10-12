package buildcache

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/wow-look-at-my/go-s3-server/cacheclient"
)

func TestDirTakesGOCACHE(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOCACHE", dir)
	got, ok := Dir()
	require.True(t, ok)
	assert.Equal(t, dir, got)
}

func TestDirRefusesOffAndRelative(t *testing.T) {
	for _, value := range []string{"off", "relative/cache"} {
		t.Setenv("GOCACHE", value)
		_, ok := Dir()
		assert.False(t, ok, value)
	}
}

func TestDirReadsTheGoEnvFile(t *testing.T) {
	want := filepath.Join(t.TempDir(), "from-env-file")
	envFile := filepath.Join(t.TempDir(), "env")
	require.NoError(t, os.WriteFile(envFile, []byte("GOPRIVATE=example.com\nGOCACHE="+want+"\n"), 0o644))
	t.Setenv("GOCACHE", "")
	t.Setenv("GOENV", envFile)
	got, ok := Dir()
	require.True(t, ok)
	assert.Equal(t, want, got)
}

func TestDirDefaultsUnderTheUserCacheDir(t *testing.T) {
	t.Setenv("GOCACHE", "")
	t.Setenv("GOENV", "off")
	base, err := os.UserCacheDir()
	require.NoError(t, err)
	got, ok := Dir()
	require.True(t, ok)
	assert.Equal(t, filepath.Join(base, "go-build"), got)
}

func TestGoEnvFileValueWithNoFile(t *testing.T) {
	t.Setenv("GOENV", filepath.Join(t.TempDir(), "missing"))
	assert.Empty(t, goEnvFileValue("GOCACHE"))
}

func TestOpenWithNoStoreOpensNothing(t *testing.T) {
	t.Serial()
	t.Setenv(cacheclient.ConfigEnv, "")
	t.Setenv(cacheclient.BrokerEnv, "")
	require.NoError(t, Open())
	assert.Empty(t, os.Getenv(cacheclient.BrokerEnv))
	Close()
}

func TestOpenServesTheCacheUntilClose(t *testing.T) {
	t.Serial()
	srv := newCacheServer(t)
	dir := filepath.Join(t.TempDir(), "gocache")
	t.Setenv(cacheclient.ConfigEnv, srv.config(t))
	t.Setenv(cacheclient.BrokerEnv, "")
	t.Setenv("GOCACHE", dir)
	t.Setenv(cacheLogEnv, "")

	require.NoError(t, Open())
	require.NoError(t, Open())
	assert.NotEmpty(t, os.Getenv(cacheclient.BrokerEnv))
	assert.Equal(t, dir, os.Getenv("GOCACHE"))
	assert.DirExists(t, dir)

	Close()
	assert.Empty(t, os.Getenv(cacheclient.BrokerEnv))
	Close()
}

func TestNoticeWritesTheCacheLog(t *testing.T) {
	t.Serial()
	logPath := filepath.Join(t.TempDir(), "gocache.log")
	t.Setenv(cacheLogEnv, logPath)
	noticeOnce = sync.Once{}
	noticeFile = nil
	t.Cleanup(func() {
		if noticeFile != nil {
			noticeFile.Close()
		}
		noticeOnce = sync.Once{}
		noticeFile = nil
	})

	noticeLogger{}.Infof("index: %d keys", 7)
	noticeLogger{}.Warnf("refresh failed")
	noticeLogger{}.Debugf("never written")

	got, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.Contains(t, string(got), "index: 7 keys")
	assert.Contains(t, string(got), "refresh failed")
	assert.NotContains(t, string(got), "never written")
}
