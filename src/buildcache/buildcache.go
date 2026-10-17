// Package buildcache makes the pipeline the owner of the build cache that
// every go command it starts uses.
//
// The go command opens the cache through cacheclient.OpenCache. The pipeline
// starts its go commands side by side. The pipeline opens the cache before it
// starts any of them, and each of them is a child of it.
package buildcache

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/wow-look-at-my/go-s3-server/cacheclient"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// cacheLogEnv names a file that takes the cache's notices in place of stderr. The go command reads the same variable.
const cacheLogEnv = "GOCACHELOG"

var (
	openMu sync.Mutex
	opened bool
)

// Open opens the build cache in this process and serves it to every process
// this starts, when GO_BUILDCACHE_CONFIG names a store. With no store
// configured it opens nothing, and each go command decides for itself, which
// includes refusing a CI build that has no store.
//
// GOCACHE is set to the directory opened, because a go command refuses an
// owner that writes into a directory other than the one it resolved.
func Open() error {
	openMu.Lock()
	defer openMu.Unlock()
	if opened {
		return nil
	}
	cfg := cacheclient.ConfigFromEnv()
	if cfg.Bucket == "" {
		return nil
	}
	dir, ok := Dir()
	if !ok {
		return nil
	}
	if err := os.MkdirAll(dir, 0o777); err != nil {
		// A directory this process cannot create is a directory its go
		// commands cannot create either, and each of them reports that itself
		// when it runs. Failing here instead would take a run that never
		// starts a go command -- an empty directory, a version query -- down
		// with a cache error in place of its own answer.
		cacheLog().Warn("shared build cache disabled: %v", err)
		return nil
	}
	os.Setenv("GOCACHE", dir)

	cacheclient.SetLogger(noticeLogger{})
	cfg.Target = runtime.GOOS + "/" + runtime.GOARCH
	cfg.Version = runtime.Version()
	cfg.Module = gomod.ReadModulePath(".")
	cfg.IndexDir = dir
	store, err := cacheclient.NewWebBackend(cfg)
	if err != nil {
		// The go command takes the same failure as a slower build.
		cacheLog().Warn("shared build cache disabled: %v", err)
		store = nil
	}
	if _, err := cacheclient.OpenCache(dir, store); err != nil {
		return fmt.Errorf("opening the build cache at %s: %w", dir, err)
	}
	opened = true
	return nil
}

// Close drains the uploads the go commands handed this process and releases
// the shared-memory names. It runs after every go command this process
// started has exited, and does nothing when Open opened nothing.
func Close() {
	openMu.Lock()
	defer openMu.Unlock()
	if !opened {
		return
	}
	opened = false
	if err := cacheclient.CloseStore(); err != nil {
		cacheLog().Error("closing the shared build cache: %v", err)
	}
	if err := cacheclient.Exit(); err != nil {
		cacheLog().Error("releasing the build cache owner: %v", err)
	}
}

// Dir answers the build cache directory the go command resolves in this
// environment. GOCACHE from the environment, then from the go env file,
// then the go-build directory under the user cache directory. It answers
// false when the cache is off or GOCACHE is not an absolute path, which the
// go command reports itself.
func Dir() (string, bool) {
	value := os.Getenv("GOCACHE")
	if value == "" {
		value = goEnvFileValue("GOCACHE")
	}
	if value == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			return "", false
		}
		return filepath.Join(base, "go-build"), true
	}
	if value == "off" || !filepath.IsAbs(value) {
		return "", false
	}
	return value, true
}

// goEnvFileValue answers key's value in the file go env -w writes, or "".
func goEnvFileValue(key string) string {
	path := goEnvFile()
	if path == "" {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, value, ok := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		if ok && strings.TrimSpace(name) == key {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// goEnvFile answers the path of the go env file, or "" when there is none.
func goEnvFile() string {
	if path := os.Getenv("GOENV"); path != "" {
		if path == "off" {
			return ""
		}
		return path
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "go", "env")
}

// noticeLogger writes the cache's notices where the go command writes its own: the GOCACHELOG file when one is named.
type noticeLogger struct{}

var (
	noticeOnce sync.Once
	noticeFile *os.File
)

func notice(warn bool, format string, args ...any) {
	noticeOnce.Do(func() {
		path := os.Getenv(cacheLogEnv)
		if path == "" {
			return
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o666)
		if err != nil {
			cacheLog().Warn("%s: %v; the cache notices go to the console", cacheLogEnv, err)
			return
		}
		noticeFile = file
	})
	if noticeFile != nil {
		stamp := fmt.Sprintf("%s [%d] ", time.Now().Format(time.TimeOnly), os.Getpid())
		fmt.Fprintf(noticeFile, stamp+format+"\n", args...)
		return
	}
	if warn {
		cacheLog().Warn(format, args...)
		return
	}
	cacheLog().Info(format, args...)
}

// cacheLog is the logger the cache's console notices go through, prefixed
// as the go command prefixes its own.
func cacheLog() *logger.Logger {
	return logger.WithSubsystem("go")
}

func (noticeLogger) Infof(format string, args ...any) { notice(false, format, args...) }

func (noticeLogger) Warnf(format string, args ...any) { notice(true, format, args...) }

func (noticeLogger) Debugf(string, ...any) {}
