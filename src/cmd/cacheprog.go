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
	"github.com/wow-look-at-my/go-toolchain/src/logger"
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
	// Accept both standard and URL-safe base64, with or without padding,
	// and with or without line wrapping (76-char lines).
	normalized := strings.NewReplacer("-", "+", "_", "/", "\n", "", "\r", "", " ", "").Replace(raw)
	if m := len(normalized) % 4; m != 0 {
		normalized += strings.Repeat("=", 4-m)
	}
	cacheProgLog := logger.WithSubsystem("cache")
	data, err := base64.StdEncoding.DecodeString(normalized)
	if err != nil {
		cacheProgLog.Debug("GO_BUILDCACHE_CONFIG: base64 decode error: %v", err)
		return cache.WebConfig{}
	}
	var cfg buildCacheConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		cacheProgLog.Debug("GO_BUILDCACHE_CONFIG: json unmarshal error: %v", err)
		return cache.WebConfig{}
	}
	if cfg.Endpoint == "" {
		cacheProgLog.Debug("GO_BUILDCACHE_CONFIG: missing endpoint field")
		return cache.WebConfig{}
	}
	if cfg.KeyID == "" || cfg.AccessKey == "" {
		cacheProgLog.Debug("GO_BUILDCACHE_CONFIG: missing key_id or access_key")
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
			logger.WithSubsystem("cache").Debug("web init error: %v (continuing local-only)", err)
		} else if web != nil {
			endpoint := cfg.Endpoint
			if endpoint == "" {
				endpoint = "(default)"
			}
			logger.WithSubsystem("cache").Debug("web enabled bucket=%s endpoint=%s", cfg.Bucket, endpoint)
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
		logger.Warn("GOCACHEPROG requires Go 1.24+; buildcache disabled")
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
	d, remoteEndpoint, err := startCacheDaemon(daemonSock)
	if err != nil {
		cacheSetupErr = fmt.Errorf("cache daemon: %w", err)
		return nil
	}
	cacheDaemon = d
	os.Setenv("GOCACHE_DAEMON_SOCK", daemonSock)
	if remoteEndpoint != "" {
		sl.SetHasRemote()
		logger.WithSubsystem("cache").Debug("remote enabled endpoint=%s", remoteEndpoint)
	} else {
		logger.WithSubsystem("cache").Debug("local only")
	}

	os.Setenv("GOCACHEPROG", exe+" cacheprog")
	return nil
}

// startCacheDaemon creates a cache daemon with local + web backends.
// Returns the daemon, the remote endpoint (empty if no remote), and any error.
func startCacheDaemon(sockPath string) (*cache.Daemon, string, error) {
	cacheDir := filepath.Join(cacheHome(), "buildcache")
	local, err := cache.NewLocalCache(cacheDir)
	if err != nil {
		return nil, "", err
	}

	cfg := parseBuildCacheConfig()
	var remote cache.IBackend
	web, err := cache.NewWebBackend(cfg)
	if err != nil {
		logger.WithSubsystem("cache").Debug("daemon web error: %v (continuing local-only)", err)
	} else if web != nil {
		remote = web
	}

	d, err := cache.NewDaemon(sockPath, local, remote)
	if err != nil {
		return nil, "", err
	}
	endpoint := ""
	if remote != nil {
		endpoint = cfg.Endpoint
	}
	return d, endpoint, nil
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
		logger.Warn("%s", msg)
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
			logger.Info("⇒ Cache: disabled (requires Go 1.%d+)", FeatureCacheProg.MinorVersion)
		case cacheSetupErr != nil:
			logger.Info("⇒ Cache: disabled (%v)", cacheSetupErr)
		default:
			logger.Info("⇒ Cache: disabled")
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
	if stats.Batch != nil {
		populated := stats.Batch.Populated.Load()
		if populated > 0 {
			parts = append(parts, fmt.Sprintf("prefetched %d", populated))
		}
	}

	logger.Info("⇒ Cache: %s", strings.Join(parts, "  "))

	// Print latency profile if any operations were recorded.
	if stats.Latency != nil {
		snap := stats.Latency.Snapshot()
		type row struct {
			name string
			s    cache.LatencySnapshot
		}
		rows := []row{
			{"lock wait", snap.LockWait},
			{"local get", snap.LocalGet},
			{"local put", snap.LocalPut},
			{"remote get", snap.RemoteGet},
			{"  http get", snap.HTTPGet},
			{"  decomp", snap.Decompress},
			{"remote put", snap.RemotePut},
			{"  sem wait", snap.SemWait},
			{"  compress", snap.Compress},
			{"  http put", snap.HTTPPut},
		}
		var hasData bool
		for _, r := range rows {
			if r.s.Count > 0 {
				hasData = true
				break
			}
		}
		if hasData {
			logger.Info("    Latency (min/avg/max):")
			for _, r := range rows {
				if r.s.Count == 0 {
					continue
				}
				logger.Info("      %-10s  %s  (n=%d)", r.name, r.s.FormatMs(), r.s.Count)
			}
		}
	}
	if stats.Pool != nil {
		poolSnap := stats.Pool.Snapshot()
		if poolSnap.Samples > 0 {
			avg := poolSnap.AvgUsed()
			logger.Info("    Pool: peak %d/%d (%.0f%%)  avg %.1f/%d (%.0f%%)",
				poolSnap.Peak, cache.MaxConnsPerHost,
				float64(poolSnap.Peak)/float64(cache.MaxConnsPerHost)*100,
				avg, cache.MaxConnsPerHost,
				avg/float64(cache.MaxConnsPerHost)*100)
		}
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
