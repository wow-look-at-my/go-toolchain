package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

// buildCacheConfig is the JSON structure inside GO_BUILDCACHE_CONFIG.
type buildCacheConfig struct {
	Endpoint  string `json:"endpoint"`
	Bucket    string `json:"bucket"`
	KeyID     string `json:"key_id"`
	AccessKey string `json:"access_key"`
}

// parseBuildCacheConfig reads web cache configuration from GO_BUILDCACHE_CONFIG
// (base64-encoded JSON) or falls back to individual env vars.
func parseBuildCacheConfig() cache.WebConfig {
	raw := os.Getenv("GO_BUILDCACHE_CONFIG")
	if raw == "" {
		return cache.WebConfig{}
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return cache.WebConfig{}
	}
	var cfg buildCacheConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cache.WebConfig{}
	}
	bucket := cfg.Bucket
	if bucket == "" {
		bucket = "gobuildcache"
	}
	return cache.WebConfig{
		Bucket:    bucket,
		Endpoint:  cfg.Endpoint,
		AccessKey: cfg.KeyID,
		SecretKey: cfg.AccessKey,
		Version:   buildVersion,
	}
}

func runCacheProg(cmd *cobra.Command, args []string) error {
	// Fast path: if a cache daemon is running, proxy to it.
	// This avoids re-loading the web index for every go subprocess.
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
	cfg := parseBuildCacheConfig()
	{
		web, err := cache.NewWebBackend(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cacheprog: web init error: %v (continuing local-only)\n", err)
		} else if web != nil {
			endpoint := cfg.Endpoint
			if endpoint == "" {
				endpoint = "(default)"
			}
			fmt.Fprintf(os.Stderr, "cacheprog: web enabled bucket=%s endpoint=%s\n", cfg.Bucket, endpoint)
			remote = web
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

// goSupportsFeature returns true if the resolved Go toolchain is new enough
// to support the given feature. It uses resolvedGoMinor (set by EnsureGoVersion)
// rather than shelling out to "go version", which avoids false negatives when
// the bootstrapped Go isn't the first "go" in the original PATH.
func goSupportsFeature(f GoFeature) bool {
	return resolvedGoMinor >= f.MinorVersion
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
// don't each re-load the web index.
var cacheDaemon *cache.Daemon

// cacheEnabled tracks whether enableCacheProg was called (even if setup failed).
// cacheSetupErr records why cache setup failed, if it did.
var (
	cacheEnabled  bool
	cacheSetupErr error
)

func enableCacheProg() error {
	cacheEnabled = true
	if err := validateCICacheConfig(); err != nil {
		return err
	}

	if !goSupportsFeature(FeatureCacheProg) {
		fmt.Fprintf(os.Stderr, "Warning: GOCACHEPROG requires Go 1.24+; buildcache disabled\n")
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		cacheSetupErr = fmt.Errorf("resolve executable: %w", err)
		return nil
	}

	// Create stats socket first — the daemon's Server needs it.
	sockPath := filepath.Join(os.TempDir(), fmt.Sprintf("gocache-stats-%d.sock", os.Getpid()))
	sl, err := cache.NewStatsListener(sockPath)
	if err != nil {
		cacheSetupErr = fmt.Errorf("stats listener: %w", err)
		return nil
	}
	statsListener = sl
	os.Setenv("GOCACHE_STATS_SOCK", sockPath)

	// Start cache daemon so child go processes share a single web index.
	daemonSock := filepath.Join(os.TempDir(), fmt.Sprintf("gocache-daemon-%d.sock", os.Getpid()))
	if d, err := startCacheDaemon(daemonSock); err == nil {
		cacheDaemon = d
		os.Setenv("GOCACHE_DAEMON_SOCK", daemonSock)
	}

	os.Setenv("GOCACHEPROG", exe+" cacheprog")
	return nil
}

// startCacheDaemon creates a cache daemon with local + web backends.
func startCacheDaemon(sockPath string) (*cache.Daemon, error) {
	cacheDir := filepath.Join(cacheHome(), "buildcache")
	local, err := cache.NewLocalCache(cacheDir)
	if err != nil {
		return nil, err
	}

	var remote cache.IBackend
	web, err := cache.NewWebBackend(parseBuildCacheConfig())
	if err != nil {
		fmt.Fprintf(os.Stderr, "cacheprog: daemon web error: %v (continuing local-only)\n", err)
	} else if web != nil {
		remote = web
	}

	return cache.NewDaemon(sockPath, local, remote)
}

// validateCICacheConfig checks that web caching env vars are configured when
// running in CI. Returns an error if any are missing, unless
// GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED=1 is set (downgrades to warning).
func validateCICacheConfig() error {
	if os.Getenv("CI") == "" {
		return nil
	}

	if os.Getenv("GO_BUILDCACHE_CONFIG") != "" {
		return nil
	}

	msg := "CI caching not configured: GO_BUILDCACHE_CONFIG is not set"
	if os.Getenv("GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED") == "1" {
		fmt.Fprintf(os.Stderr, "Warning: %s\n", msg)
		return nil
	}
	return fmt.Errorf("%s\n  Set GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED=1 to downgrade to warning", msg)
}

func printCacheStats(close bool) {
	if close && cacheDaemon != nil {
		cacheDaemon.Close()
	}
	if statsListener == nil {
		switch {
		case !cacheEnabled:
			return
		case !goSupportsFeature(FeatureCacheProg):
			fmt.Printf("==> Cache: disabled (requires Go 1.%d+)\n", FeatureCacheProg.MinorVersion)
		case cacheSetupErr != nil:
			fmt.Printf("==> Cache: disabled (%v)\n", cacheSetupErr)
		default:
			fmt.Printf("==> Cache: disabled\n")
		}
		return
	}
	if close {
		statsListener.Close()
	}

	stats := statsListener.Stats()

	hits := stats.Local.Hits.Load()
	puts := stats.Local.Puts.Load()
	misses := stats.Misses.Load()

	var parts []string
	parts = append(parts, fmt.Sprintf("\u2193 %d", hits))
	parts = append(parts, fmt.Sprintf("\u2191 %d", puts))
	if stats.Remote != nil {
		parts = append(parts, fmt.Sprintf("\ueac2 %d", stats.Remote.Hits.Load()))
		parts = append(parts, fmt.Sprintf("\ueac3 %d", stats.Remote.Puts.Load()))
	}
	parts = append(parts, fmt.Sprintf("miss %d", misses))

	total := hits + misses
	if total > 0 {
		rate := float64(hits) / float64(total) * 100
		parts = append(parts, fmt.Sprintf("(%.0f%% hit)", rate))
	}

	fmt.Printf("==> Cache: %s\n", strings.Join(parts, "  "))
}

// cacheHome returns the base cache directory (~/.cache/go-toolchain).
func cacheHome() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "go-toolchain")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "go-toolchain")
}
