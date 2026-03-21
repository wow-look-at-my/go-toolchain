package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/cache"
)

func init() {
	cmd := &cobra.Command{
		Use:    "cacheprog",
		Short:  "GOCACHEPROG protocol server (internal)",
		Hidden: true,
		RunE:   runCacheProg,
	}
	rootCmd.AddCommand(cmd)
}

func runCacheProg(cmd *cobra.Command, args []string) error {
	// Fast path: if a cache daemon is running, proxy to it.
	// This avoids re-loading the S3 index for every go subprocess.
	if sock := os.Getenv("GOCACHE_DAEMON_SOCK"); sock != "" {
		if err := cache.ProxyToDaemon(sock); err == nil {
			return nil
		}
		// Daemon unavailable — fall through to standalone mode.
	}

	cacheDir := filepath.Join(cacheHome(), "buildcache")

	local, err := cache.NewLocalCache(cacheDir)
	if err != nil {
		return fmt.Errorf("local cache: %w", err)
	}

	var remote cache.IBackend
	bucket := os.Getenv("GOCACHE_S3_BUCKET")
	if bucket == "" {
		bucket = "gobuildcache"
	}
	{
		s3, err := cache.NewS3Backend(cache.S3Config{
			Bucket:   bucket,
			Region:   os.Getenv("GOCACHE_S3_REGION"),
			Endpoint: os.Getenv("GOCACHE_S3_ENDPOINT"),
			Prefix:   os.Getenv("GOCACHE_S3_PREFIX"),
			AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
			SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "cacheprog: s3 init error: %v (continuing local-only)\n", err)
		} else if s3 != nil {
			endpoint := os.Getenv("GOCACHE_S3_ENDPOINT")
			if endpoint == "" {
				endpoint = "AWS"
			}
			fmt.Fprintf(os.Stderr, "cacheprog: s3 enabled bucket=%s endpoint=%s\n", bucket, endpoint)
			remote = s3
		}
	}

	srv := cache.NewServer(local, remote)
	return srv.Run(os.Stdin, os.Stdout)
}

// GoFeature represents a Go toolchain feature with a minimum version requirement.
type GoFeature struct {
	Name         string
	MinorVersion int // minimum Go 1.X version required
}

var (
	FeatureCacheProg = GoFeature{Name: "GOCACHEPROG", MinorVersion: 24}
)

// goSupportsFeature returns true if the go binary in PATH is new enough
// to support the given feature.
func goSupportsFeature(f GoFeature) bool {
	out, err := exec.Command("go", "version").Output()
	if err != nil {
		return false
	}
	// Output: "go version go1.24.2 darwin/arm64"
	fields := strings.Fields(string(out))
	if len(fields) < 3 {
		return false
	}
	ver := strings.TrimPrefix(fields[2], "go")
	parts := strings.SplitN(ver, ".", 3)
	if len(parts) < 2 {
		return false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return maj > 1 || (maj == 1 && min >= f.MinorVersion)
}

// enableCacheProg sets the GOCACHEPROG environment variable so all child go
// processes use this binary as the cache program server.
// Requires Go 1.24+; on older versions it prints a warning and returns.
// If the executable path can't be resolved, it silently falls back to
// Go's default cache behavior.
// statsListener is the unix socket listener that aggregates stats from
// all cacheprog subprocesses. Created by enableCacheProg, read by printCacheStats.
var statsListener *cache.StatsListener

// cacheDaemon is the shared cache daemon started by enableCacheProg.
// It serves GOCACHEPROG requests over a Unix socket so child processes
// don't each re-load the S3 index.
var cacheDaemon *cache.Daemon

func enableCacheProg() error {
	if err := validateCICacheConfig(); err != nil {
		return err
	}

	if !goSupportsFeature(FeatureCacheProg) {
		fmt.Fprintf(os.Stderr, "Warning: GOCACHEPROG requires Go 1.24+; buildcache disabled\n")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil
	}

	// Create stats socket first — the daemon's Server needs it.
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("gocache-stats-%d.sock", os.Getpid()))
	sl, err := cache.NewStatsListener(sockPath)
	if err != nil {
		return nil
	}
	statsListener = sl
	os.Setenv("GOCACHE_STATS_SOCK", sockPath)

	// Start cache daemon so child go processes share a single S3 index.
	daemonSock := filepath.Join(os.TempDir(), fmt.Sprintf("gocache-daemon-%d.sock", os.Getpid()))
	if d, err := startCacheDaemon(daemonSock); err == nil {
		cacheDaemon = d
		os.Setenv("GOCACHE_DAEMON_SOCK", daemonSock)
	}

	os.Setenv("GOCACHEPROG", exe+" cacheprog")
	return nil
}

// startCacheDaemon creates a cache daemon with local + S3 backends.
func startCacheDaemon(sockPath string) (*cache.Daemon, error) {
	cacheDir := filepath.Join(cacheHome(), "buildcache")
	local, err := cache.NewLocalCache(cacheDir)
	if err != nil {
		return nil, err
	}

	var remote cache.IBackend
	bucket := os.Getenv("GOCACHE_S3_BUCKET")
	if bucket == "" {
		bucket = "gobuildcache"
	}
	s3, err := cache.NewS3Backend(cache.S3Config{
		Bucket:    bucket,
		Region:    os.Getenv("GOCACHE_S3_REGION"),
		Endpoint:  os.Getenv("GOCACHE_S3_ENDPOINT"),
		Prefix:    os.Getenv("GOCACHE_S3_PREFIX"),
		AccessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
		SecretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: daemon s3 error: %v (continuing local-only)\n", err)
	} else if s3 != nil {
		remote = s3
	}

	return cache.NewDaemon(sockPath, local, remote)
}

// validateCICacheConfig checks that S3 caching env vars are configured when
// running in CI. Returns an error if any are missing, unless
// GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED=1 is set (downgrades to warning).
func validateCICacheConfig() error {
	if os.Getenv("CI") == "" {
		return nil
	}

	required := []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"GOCACHE_S3_ENDPOINT",
		"GOCACHE_S3_REGION",
	}

	var missing []string
	for _, v := range required {
		if os.Getenv(v) == "" {
			missing = append(missing, v)
		}
	}
	if len(missing) == 0 {
		return nil
	}

	msg := fmt.Sprintf("CI caching not configured: missing env vars: %s", strings.Join(missing, ", "))
	if os.Getenv("GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED") == "1" {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", msg)
		return nil
	}
	return fmt.Errorf("%s\n  Set GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED=1 to downgrade to warning", msg)
}

func printCacheStats() {
	if cacheDaemon != nil {
		cacheDaemon.Close()
	}
	if statsListener == nil {
		return
	}
	statsListener.Close()

	stats := statsListener.Stats()

	var parts []string
	if h := stats.Local.Hits.Load(); h > 0 {
		parts = append(parts, fmt.Sprintf("\u2193 %d", h))
	}
	if p := stats.Local.Puts.Load(); p > 0 {
		parts = append(parts, fmt.Sprintf("\u2191 %d", p))
	}
	if stats.Remote != nil {
		if h := stats.Remote.Hits.Load(); h > 0 {
			parts = append(parts, fmt.Sprintf("\ueac2 %d", h))
		}
		if p := stats.Remote.Puts.Load(); p > 0 {
			parts = append(parts, fmt.Sprintf("\ueac3 %d", p))
		}
	}
	if m := stats.Misses.Load(); m > 0 {
		parts = append(parts, fmt.Sprintf("miss %d", m))
	}

	if len(parts) > 0 {
		fmt.Printf("==> Cache: %s\n", strings.Join(parts, "  "))
	}
}

// cacheHome returns the base cache directory (~/.cache/go-toolchain).
func cacheHome() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "go-toolchain")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "go-toolchain")
}
