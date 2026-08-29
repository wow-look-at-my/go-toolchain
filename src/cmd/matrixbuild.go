package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/cache"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/profile"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// hostRunnableArtifact returns the artifact in outDir that runs on this host.
// That is the fat APE, which runs on every host by construction and is the
// only native output. Returned even when it does not exist, so callers report
// a missing artifact rather than a wrong one.
func hostRunnableArtifact(target build.Target, outDir string) string {
	return filepath.Join(outDir, build.BinaryName(target.OutputName, cosmoOS, cosmoFatArch))
}

func createHostSymlinks(targets []build.Target, outDir string) error {
	// hostos, not runtime: a cosmo fat APE reports runtime.GOOS=="cosmo" everywhere.
	hostOS := hostos.GOOS()

	for _, target := range targets {
		hostPath := hostRunnableArtifact(target, outDir)
		hostBinary := filepath.Base(hostPath)
		ext := ""
		if hostOS == "windows" {
			ext = ".exe"
		}

		// Verify the host binary exists in the output directory
		if _, err := os.Stat(hostPath); err != nil {
			logger.Info("  SKIP symlink for %s (host binary %s not found)", target.OutputName, hostBinary)
			continue
		}

		// Create <name>_host and <name> symlinks (relative, pointing to the host binary)
		for _, suffix := range []string{"_host", ""} {
			linkName := target.OutputName + suffix + ext
			linkPath := filepath.Join(outDir, linkName)
			// A cosmo build writes the APE under the plain name, so it is a real
			// binary, not a link slot -- overwriting it would delete the artifact.
			if st, statErr := os.Lstat(linkPath); statErr == nil && st.Mode()&os.ModeSymlink == 0 {
				continue
			}
			os.Remove(linkPath) // remove any stale symlink
			if err := os.Symlink(hostBinary, linkPath); err != nil {
				return fmt.Errorf("failed to create symlink %s: %w", linkName, err)
			}
			logger.Info("  LINK %s -> %s", linkPath, hostBinary)
		}
	}
	return nil
}

// checkPortableJob refuses a job that would compile anything other than the
// fat APE or a wasm binary, or would compile it with anything other than the
// gosmopolitan toolchain. Both outputs run everywhere, so nothing this
// pipeline builds is locked to one platform, and the fork is where the org's
// fixes live -- upstream's wasm support is not maintained.
//
// This sits at the one chokepoint that compiles anything, so a call site that
// invents a target fails here instead of quietly shipping a native binary.
func checkPortableJob(job buildJob) error {
	if job.forkGoroot == "" {
		return fmt.Errorf("build for %s/%s has no gosmopolitan GOROOT: the fat APE and the wasm targets are the only outputs, and both compile with the fork", job.goos, job.goarch)
	}
	if job.goos == cosmoOS || (isWasmGOOS(job.goos) && job.goarch == wasmArch) {
		return nil
	}
	return fmt.Errorf("refusing to build GOOS=%s GOARCH=%s: this pipeline builds the cosmo fat APE (one binary for every host) and the wasm targets, so a per-platform native binary has no build path", job.goos, job.goarch)
}

// runBuild compiles a single binary. If onFirstOutput is non-nil, it is
// called as soon as the compiler produces output (used for progress
// indicators on the default build path).
//
// The compiler never writes onto the target file (job.outputPath) directly:
// its -o is the .tmp- spelling of that path (build.TmpPrefix), and only the
// commit after the build succeeded moves the results onto the target name.
// A failing or killed build can therefore never leave even a partial binary
// at build/<name> for an agent or a later phase to pick up.
func runBuild(r runner.CommandRunner, job buildJob, onFirstOutput func()) error {
	if err := checkPortableJob(job); err != nil {
		return err
	}
	// Last-chokepoint guard: a fork-toolchain job MUST carry a cache namespace
	// (buildJob.cacheNamespace), so a call site that forgets to fingerprint the
	// toolchain fails loudly instead of reopening cross-toolchain cache poisoning.
	if job.cacheNamespace == "" {
		return fmt.Errorf("fork-toolchain build for %s/%s has no cache namespace; refusing to share the un-namespaced cache (see forkToolchainCacheNamespace)", job.goos, job.goarch)
	}
	args := []string{"build"}
	// Dump the action graph for the build profile (a file per invocation;
	// matrix targets each get their own). No-op when profiling is off.
	if garg := profile.GraphArg(); garg != "" {
		args = append(args, garg)
	}
	if onFirstOutput != nil {
		args = append(args, "-v") // print packages as they are compiled
	}
	if job.ldflags != "" {
		args = append(args, "-ldflags", job.ldflags)
	}
	// -o is the temp spelling; the commit below is what makes the target exist.
	args = append(args, "-o", build.TempOutputPath(job.outputPath), job.srcPath)
	goCmd := "go"
	if job.forkGoroot != "" {
		goCmd = filepath.Join(job.forkGoroot, "bin", "go")
	}
	// Every variable that picks the compiler or the output platform is
	// ASSIGNED here, never inherited: a GOOS in the ambient environment is the
	// one remaining way to ask this pipeline for a per-platform native binary.
	// CGO_ENABLED=0 always -- neither output has cgo. The cache namespace keys
	// the build to this toolchain, since the fork's constant version stamp
	// would otherwise collide action IDs across fork builds.
	cmd := runner.Cmd(goCmd, args...).
		WithEnv("GOTOOLCHAIN", "local").
		WithEnv("GOROOT", job.forkGoroot).
		WithEnv("PATH", filepath.Join(job.forkGoroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH")).
		WithEnv("CGO_ENABLED", "0").
		WithEnv(cache.KeyNamespaceEnv, job.cacheNamespace)
	if job.goos == cosmoOS {
		// GOARCH and GOCOSMOFAT are cleared: "fat" is a pseudo-arch, not a
		// real GOARCH, and an inherited GOCOSMOFAT=0 must not silently produce
		// a thin binary. GOCOSMOPLATFORMS is always assigned so the fork emits
		// only the platforms asked for.
		cmd = cmd.WithEnv("GOOS", cosmoOS).
			WithEnv("GOARCH", "").
			WithEnv("GOCOSMOFAT", "").
			WithEnv(cosmoPlatformsEnv, job.cosmoPlatforms)
	} else {
		cmd = cmd.WithEnv("GOOS", job.goos).WithEnv("GOARCH", job.goarch)
	}
	if onFirstOutput != nil {
		cmd = cmd.WithOnFirstOutput(onFirstOutput)
		if activeMissTracker != nil {
			cmd = cmd.WithStderrWriter(activeMissTracker)
		}
	} else {
		cmd = cmd.WithQuiet()
	}
	proc, err := cmd.Run(r)
	if err == nil {
		if onFirstOutput != nil {
			// Non-quiet: Wait() streams -v output to console; compiler errors go to stderr.
			err = proc.Wait()
		} else {
			// Quiet (matrix): drain pipes manually, capture stderr for error messages
			io.Copy(io.Discard, proc.Stdout())
			stderr, _ := io.ReadAll(proc.Stderr())
			if err = proc.Wait(); err != nil && len(stderr) > 0 {
				err = fmt.Errorf("%w\n%s", err, stderr)
			}
		}
	}
	if err != nil {
		// The target itself was never written, so it stays absent.
		build.DiscardOutput(job.outputPath)
		return err
	}
	// Only now do the outputs take the target's name.
	return build.CommitOutput(job.outputPath)
}
