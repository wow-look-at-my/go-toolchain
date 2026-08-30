package cmd

import (
	"fmt"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// forkBuildEnv is everything a build job takes from the gosmopolitan
// toolchain. Every build in this pipeline is a fork build, so both the default
// build phase and the matrix path resolve it through here rather than each
// assembling their own half of it.
type forkBuildEnv struct {
	goroot string
	// The fork's version stamp is constant, so builds collide without this key.
	cacheNamespace string
	// GOCOSMOPLATFORMS; empty is the fork's everything-default, and wasm-only.
	apePlatforms string
	// The platform set the APE runs on, for the manifest and logs.
	coverage []buildPlatform
}

// resolveForkBuildEnv resolves the toolchain and the cache namespace, and --
// when the run builds an APE -- the platform set it covers. It fails rather
// than falling back: there is no other compiler to fall back to.
func resolveForkBuildEnv(wantAPE bool) (forkBuildEnv, error) {
	var env forkBuildEnv
	var err error
	if wantAPE {
		if env.coverage, err = parseCosmoPlatforms(cosmoPlatforms); err != nil {
			return env, err
		}
	}
	if env.goroot, err = ensureCosmoToolchainFunc(); err != nil {
		return env, err
	}
	if env.cacheNamespace, err = forkToolchainCacheNamespace(env.goroot); err != nil {
		return env, fmt.Errorf("fingerprinting the fork toolchain for cache isolation: %w", err)
	}
	if wantAPE {
		env.apePlatforms = cosmoPlatformsEnvValue(env.goroot, env.coverage)
	}
	return env, nil
}

// apeJob returns the fat-APE build job for a main package.
func (e forkBuildEnv) apeJob(srcPath, outputPath string) buildJob {
	return buildJob{
		goos:           cosmoOS,
		goarch:         cosmoFatArch,
		srcPath:        srcPath,
		outputPath:     outputPath,
		forkGoroot:     e.goroot,
		cacheNamespace: e.cacheNamespace,
		cosmoPlatforms: e.apePlatforms,
	}
}

// warnCGOUnavailable says so when --cgo was asked for. Neither output this
// pipeline produces has cgo, so the flag changes nothing about the build, and
// a silently ignored flag reads as a working flag.
func warnCGOUnavailable(hasAPE, hasWasm bool) {
	if !cgoEnabled {
		return
	}
	if hasAPE {
		logger.Warn("⇒ Warning: --cgo has no effect on the cosmo target (cosmopolitan has no cgo; CGO_ENABLED=0 is forced)")
	}
	if hasWasm {
		logger.Warn("⇒ Warning: --cgo has no effect on wasm targets (WebAssembly has no cgo; CGO_ENABLED=0 is forced)")
	}
}
