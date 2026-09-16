package cmd

import (
	"fmt"
	"os"

	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// forkBuildEnv is everything a build job takes from the toolchain this binary
// links. Both the default build phase and the matrix path resolve it through
// here rather than each assembling their own half of it.
type forkBuildEnv struct {
	// goCmd starts the go command: this executable under its go subcommand.
	goCmd []string
	// goroot is the GOROOT the go command reads its standard library from.
	goroot string
	// GOCOSMOPLATFORMS; empty is the fork's everything-default, and wasm-only.
	apePlatforms string
	// The platform set the APE runs on, for the manifest and logs.
	coverage []buildPlatform
	// selfHosted marks a build of this pipeline's own module, which builds in passes.
	selfHosted bool
}

// resolveForkBuildEnv resolves the go command this binary is and, when the
// run builds an APE, the platform set it covers. It fails rather than falling
// back: there is no other compiler to fall back to.
func resolveForkBuildEnv(wantAPE bool) (forkBuildEnv, error) {
	var env forkBuildEnv
	var err error
	if wantAPE {
		if env.coverage, err = parseCosmoPlatforms(cosmoPlatforms); err != nil {
			return env, err
		}
	}
	if len(activeGoCmd) == 0 {
		return env, fmt.Errorf("no go command is set up for this run: EnsureGoVersion has to run first")
	}
	env.goCmd = activeGoCmd
	env.goroot = activeGoroot
	env.selfHosted = ownModule()
	if wantAPE {
		env.apePlatforms = cosmoPlatformsEnvValue(env.coverage)
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
		goCmd:          e.goCmd,
		goroot:         e.goroot,
		cosmoPlatforms: e.apePlatforms,
		ldflags:        jobLDFlags(srcPath, os.Getenv("GOFLAGS")),
		selfHosted:     e.selfHosted,
	}
}

// jobLDFlags is what the caller asked the linker for, plus the revision stamp.
func jobLDFlags(srcPath, goflags string) string {
	return joinLDFlags(stampLDFlags(srcPath), callerLDFlags(goflags))
}

// joinLDFlags puts the stamp ahead of the caller, so an explicit -X wins: the linker keeps the LAST value for a name.
func joinLDFlags(stamp, caller string) string {
	if stamp == "" {
		return caller
	}
	if caller == "" {
		return stamp
	}
	return stamp + " " + caller
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
