package cmd

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/wow-look-at-my/go-toolchain/src/cache"
	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	gotrace "github.com/wow-look-at-my/go-toolchain/src/trace"
	"go.opentelemetry.io/otel/trace"
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
//
// The cache server is no longer S3-compatible: it authenticates with plain HTTP
// Basic Auth, so the native credential fields are username/password. The old
// S3/AWS-style fields (key_id, access_key, region) are still accepted as
// deprecated aliases — parseBuildCacheConfig warns when they are used and they
// will be removed in a future release.
type buildCacheConfig struct {
	Endpoint string `json:"endpoint"`
	Bucket   string `json:"bucket"`

	// Native credential fields (HTTP Basic Auth).
	Username string `json:"username"`
	Password string `json:"password"`

	// Deprecated S3/AWS-style aliases.
	KeyID     string `json:"key_id"`     // deprecated alias for username
	AccessKey string `json:"access_key"` // deprecated alias for password
	Region    string `json:"region"`     // S3-only, ignored
}

// parseBuildCacheConfig reads web cache configuration from GO_BUILDCACHE_CONFIG
// (base64-encoded JSON) or falls back to individual env vars.
func parseBuildCacheConfig() cache.WebConfig {
	raw := os.Getenv("GO_BUILDCACHE_CONFIG")
	if raw == "" {
		return cache.WebConfig{}
	}
	// Accepts standard or URL-safe base64, padded or not, wrapped or not.
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

	// Prefer native username/password; fall back to deprecated S3-style aliases.
	var deprecated []string
	username := cfg.Username
	if username == "" && cfg.KeyID != "" {
		username = cfg.KeyID
		deprecated = append(deprecated, "key_id (use username)")
	}
	password := cfg.Password
	if password == "" && cfg.AccessKey != "" {
		password = cfg.AccessKey
		deprecated = append(deprecated, "access_key (use password)")
	}
	if cfg.Region != "" {
		deprecated = append(deprecated, "region (ignored; the cache server is not S3)")
	}
	if len(deprecated) > 0 {
		cacheProgLog.Warn("GO_BUILDCACHE_CONFIG: deprecated S3-style field(s): %s; these will be removed in a future release", strings.Join(deprecated, ", "))
	}
	if username == "" || password == "" {
		cacheProgLog.Warn("GO_BUILDCACHE_CONFIG: missing username or password")
		return cache.WebConfig{}
	}
	bucket := cfg.Bucket
	if bucket == "" {
		bucket = "gobuildcache"
	}
	return cache.WebConfig{
		Bucket:    bucket,
		Endpoint:  cfg.Endpoint,
		AccessKey: username,
		SecretKey: password,
		Version:   buildVersion,
		// X-Cache-Meta-Module; empty (omitted) when the CWD has no go.mod.
		Module: gomod.ReadModulePath(),
	}
}

func runCacheProg(cmd *cobra.Command, args []string) error {
	// Stdout is the GOCACHEPROG protocol pipe cmd/go parses; force stderr-only
	// logging first so no "::warning" annotation corrupts the JSON stream.
	level := logger.LevelInfo
	if os.Getenv("GOCACHE_DEBUG") == "1" {
		level = logger.LevelDebug
	}
	logger.InitSubprocess(level)

	// Namespaces fork-toolchain builds (see cache.KeyNamespaceEnv) so builds
	// using different fork toolchains never share cache entries, since the
	// fork's constant version stamp would otherwise collide their action IDs.
	namespace := cache.CanonicalKeyNamespace(os.Getenv(cache.KeyNamespaceEnv))

	// Fast path: proxy to a running daemon, skipping a per-subprocess web
	// index reload. Skipped for a namespaced cacheprog: the daemon is an
	// unnamespaced raw byte pipe, and proxying would leak cache entries.
	if sock := daemonSockUnlessNamespaced(namespace); sock != "" {
		if err := cache.ProxyToDaemon(sock); err == nil {
			return nil
		}
		// Daemon unavailable — fall through to standalone mode.
	}

	cacheDir := filepath.Join(cacheHome(), "buildcache")

	// The daemon path purges via NewLocalStore; standalone must do it itself.
	cache.EnsureLocalCacheVersion(cacheDir)

	// Standalone mode uses the loose-file cache, not the FUSE store: the FUSE
	// mount is daemon-owned so concurrent standalone invocations can't collide.
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
	srv.SetKeyNamespace(namespace)
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

// goSupportsFeature checks resolvedGoMinor (set by EnsureGoVersion) instead
// of shelling out to "go version", avoiding a false negative when the
// bootstrapped Go isn't the first "go" on the original PATH.
func goSupportsFeature(f GoFeature) bool {
	return resolvedGoMinor >= f.MinorVersion
}

// statsListener aggregates stats from cacheprog subprocesses over a socket.
var statsListener *cache.StatsListener

// cacheDaemon is the shared Unix-socket daemon so children skip reloading the web index.
var cacheDaemon *cache.Daemon

// cacheEnabled: enableCacheProg ran. cacheSetupErr: why setup failed, if it did.
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

	// Set OTEL_TRACEPARENT before the daemon starts: the tracer provider reads
	// it at init to fix the timeline root span's ID, matching what cacheprog
	// subprocesses report as their parent.
	if os.Getenv("OTEL_TRACEPARENT") == "" {
		traceID, spanID := generateTraceIDs()
		traceparent := fmt.Sprintf("00-%s-%s-01", traceID.String(), spanID.String())
		os.Setenv("OTEL_TRACEPARENT", traceparent)
	}

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

	progCmd, err := cacheProgCommand(runtime.GOOS, hostos.GOOS(), exe)
	if err != nil {
		cacheSetupErr = err
		return nil
	}
	os.Setenv("GOCACHEPROG", progCmd)
	return nil
}

// cacheProgCommand returns the GOCACHEPROG value that launches this binary's
// cacheprog subcommand: normally the bare self-exec ("<exe> cacheprog").
//
// A cosmo fat APE on a macOS host cannot be fork/exec'd directly: ARM64 macOS
// never self-assimilates the APE into a native ELF (unlike Linux), so the
// file keeps its MZ polyglot magic and execve fails with ENOEXEC. cmd/go has
// no shell fallback, so every `go` invocation would die with "exec format
// error". The fix wraps the APE in a #!/bin/sh script: the shell's ENOEXEC
// fallback interprets the APE header directly.
func cacheProgCommand(goos, hostGOOS, exe string) (string, error) {
	if goos != cosmoOS || hostGOOS != "darwin" {
		return quoteExeForGOCACHEPROG(exe) + " cacheprog", nil
	}
	if strings.Contains(exe, "'") {
		// Not representable in the single-quoted wrapper; disable the cache
		// rather than misquote the exec.
		return "", fmt.Errorf("executable path %q cannot be embedded in the cacheprog wrapper", exe)
	}
	wrapper := filepath.Join(os.TempDir(), fmt.Sprintf("gocacheprog-wrapper-%d.sh", os.Getpid()))
	script := "#!/bin/sh\nexec '" + exe + "' cacheprog\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		return "", fmt.Errorf("write cacheprog wrapper: %w", err)
	}
	return quoteExeForGOCACHEPROG(wrapper), nil
}

// quoteExeForGOCACHEPROG quotes an executable path for cmd/go's GOCACHEPROG
// parser (internal/quoted.Split: space-separated words, single or double
// quotes, NO escape sequences). An unquoted path containing a space would be
// split into two argv words and the cacheprog launch would fail fatally.
// A path with no spaces or quotes is returned unchanged.
func quoteExeForGOCACHEPROG(exe string) string {
	if !strings.ContainsAny(exe, " \t'\"") {
		return exe
	}
	if !strings.Contains(exe, `"`) {
		return `"` + exe + `"`
	}
	if !strings.Contains(exe, "'") {
		return "'" + exe + "'"
	}
	// Both quote kinds present: not representable, so return as-is and let
	// the launch fail loudly rather than silently misparse.
	return exe
}

// startCacheDaemon creates a cache daemon with local + web backends.
// Returns the daemon, the remote endpoint (empty if no remote), and any error.
func startCacheDaemon(sockPath string) (*cache.Daemon, string, error) {
	cacheDir := filepath.Join(cacheHome(), "buildcache")
	// The daemon owns the FUSE mount; NewLocalStore prefers the packed FUSE
	// cache, falling back to loose files when FUSE is unavailable.
	local, err := cache.NewLocalStore(cacheDir)
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
	// Shut down the OTel tracer provider now that the daemon has drained, so
	// timeline exporter spans from src/trace.Export land in the final batch.
	if close {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = gotrace.Shutdown(ctx)
	}
	if close && statsListener != nil {
		statsListener.Close()
	}
	// Emit the build profile once daemon Close (final web counters) and
	// listener Close (final per-action events) have both drained.
	if close {
		emitBuildProfile()
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
}

// cacheHome returns the base cache directory (~/.cache/go-toolchain).
func cacheHome() string {
	if xdg := os.Getenv("XDG_CACHE_HOME"); xdg != "" {
		return filepath.Join(xdg, "go-toolchain")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "go-toolchain")
}

func generateTraceIDs() (trace.TraceID, trace.SpanID) {
	traceIDBytes := [16]byte{}
	spanIDBytes := [8]byte{}
	rand.Read(traceIDBytes[:])
	rand.Read(spanIDBytes[:])
	return trace.TraceID(traceIDBytes), trace.SpanID(spanIDBytes)
}
