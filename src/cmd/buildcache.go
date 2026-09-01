package cmd

import (
	"fmt"
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// GO_BUILDCACHE_CONFIG is gosmopolitan's own shared-cache env var
// (cacheclient.ConfigFromEnv in wow-look-at-my/go-s3-server). The fork's
// cmd/go links that client in and consults it directly, ahead of
// GOCACHEPROG, so this binary needs no cache server of its own -- it only
// has to make sure CI actually set the variable the fork reads.

// validateCICacheConfig checks that the shared build cache is configured
// when running in CI. Returns an error if it is missing, unless
// GO_TOOLCHAIN_CACHING_INTENTIONALLY_NOT_CONFIGURED downgrades it to a warning.
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
