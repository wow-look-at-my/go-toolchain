package cmd

import (
	"fmt"
	"os"
	"path/filepath"

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
	cacheDir := filepath.Join(cacheHome(), "buildcache")

	local, err := cache.NewLocalCache(cacheDir)
	if err != nil {
		return fmt.Errorf("local cache: %w", err)
	}

	var remote cache.Backend
	if bucket := os.Getenv("GOCACHE_S3_BUCKET"); bucket != "" {
		s3, err := cache.NewS3Backend(bucket, os.Getenv("GOCACHE_S3_REGION"), os.Getenv("GOCACHE_S3_PREFIX"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "cacheprog: s3 backend: %v (continuing local-only)\n", err)
		} else if s3 != nil {
			remote = cache.NewAsyncBackend(s3)
		}
	}

	srv := cache.NewServer(local, remote)
	return srv.Run(os.Stdin, os.Stdout)
}

// enableCacheProg sets GOFLAGS so all child go processes use this binary
// as the GOCACHEPROG server. If the executable path can't be resolved,
// it silently falls back to Go's default cache behavior.
func enableCacheProg() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	flag := fmt.Sprintf("-cacheprog=%s cacheprog", exe)
	if existing := os.Getenv("GOFLAGS"); existing != "" {
		os.Setenv("GOFLAGS", existing+" "+flag)
	} else {
		os.Setenv("GOFLAGS", flag)
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
