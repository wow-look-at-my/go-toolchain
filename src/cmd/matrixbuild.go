package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/cache"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/profile"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// hostRunnableArtifact returns the artifact in outDir that runs on this
// host: the native <name>_<hostos>_<hostarch> build when it exists, else the
// fat APE (which runs here by construction). Returned even when neither
// exists, so callers report a missing artifact rather than a wrong one.
func hostRunnableArtifact(target build.Target, outDir string) string {
	native := filepath.Join(outDir, build.BinaryName(target.OutputName, hostos.GOOS(), runtime.GOARCH))
	if _, err := os.Stat(native); err == nil {
		return native
	}
	ape := filepath.Join(outDir, build.BinaryName(target.OutputName, cosmoOS, cosmoFatArch))
	if _, err := os.Stat(ape); err == nil {
		return ape
	}
	return native
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

// runBuild compiles a single binary. If onFirstOutput is non-nil, it is
// called when the compiler produces its first output (used for progress
// indicators on the default build path).
//
// The compiler never writes onto the target file (job.outputPath) directly:
// its -o is the .tmp- spelling of that path (build.TmpPrefix), and only the
// commit after the build succeeded moves the results onto the target name.
// A failing or killed build can therefore never leave even a partial binary
// at build/<name> for an agent or a later phase to pick up.
func runBuild(r runner.CommandRunner, job buildJob, onFirstOutput func()) error {
	// Last-chokepoint guard: a fork-toolchain job MUST carry a cache namespace
	// (buildJob.cacheNamespace), so a call site that forgets to fingerprint the
	// toolchain fails loudly instead of reopening cross-toolchain cache poisoning.
	if job.forkGoroot != "" && job.cacheNamespace == "" {
		return fmt.Errorf("fork-toolchain build for %s/%s has no cache namespace; refusing to share the un-namespaced cache (see forkToolchainCacheNamespace)", job.goos, job.goarch)
	}
	args := []string{"build"}
	// Dump the action graph for the build profile (one file per invocation;
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
	// -o carries the temp spelling of the target file: the commit below is
	// what ever makes build/<name> exist (see build.CommitOutput).
	args = append(args, "-o", build.TempOutputPath(job.outputPath), job.srcPath)
	goCmd := "go"
	if job.forkGoroot != "" {
		goCmd = filepath.Join(job.forkGoroot, "bin", "go")
	}
	cmd := runner.Cmd(goCmd, args...)
	switch {
	case job.forkGoroot != "" && job.goos == cosmoOS:
		// GOOS=cosmo fat-APE build. GOARCH and GOCOSMOFAT are cleared: "fat"
		// is a pseudo-arch, not a real GOARCH, and an inherited GOCOSMOFAT=0
		// must not silently produce a thin binary. The cache namespace keys
		// this build to this toolchain, since the fork's constant version
		// stamp would otherwise collide action IDs across fork builds.
		// GOCOSMOPLATFORMS is always assigned so the fork builds only the
		// platforms needed.
		cmd = cmd.WithEnv("GOOS", cosmoOS).
			WithEnv("GOARCH", "").
			WithEnv("GOCOSMOFAT", "").
			WithEnv(cosmoPlatformsEnv, job.cosmoPlatforms).
			WithEnv("GOTOOLCHAIN", "local").
			WithEnv("GOROOT", job.forkGoroot).
			WithEnv("PATH", filepath.Join(job.forkGoroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH")).
			WithEnv("CGO_ENABLED", "0").
			WithEnv(cache.KeyNamespaceEnv, job.cacheNamespace)
	case job.forkGoroot != "":
		// Wasm build (js/wasm or wasip1/wasm) via the gosmopolitan toolchain.
		// The fork DEFAULTS to GOOS=cosmo, so GOOS and GOARCH are always
		// pinned explicitly. CGO_ENABLED=0 always: wasm has no cgo. The cache
		// namespace: same fork, same constant-version action-ID collisions,
		// same isolation (see the cosmo case above).
		cmd = cmd.WithEnv("GOOS", job.goos).
			WithEnv("GOARCH", job.goarch).
			WithEnv("GOTOOLCHAIN", "local").
			WithEnv("GOROOT", job.forkGoroot).
			WithEnv("PATH", filepath.Join(job.forkGoroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH")).
			WithEnv("CGO_ENABLED", "0").
			WithEnv(cache.KeyNamespaceEnv, job.cacheNamespace)
	default:
		if job.goos != "" {
			cmd = cmd.WithEnv("GOOS", job.goos)
		}
		if job.goarch != "" {
			cmd = cmd.WithEnv("GOARCH", job.goarch)
		}
	}
	if onFirstOutput != nil {
		cmd = cmd.WithOnFirstOutput(onFirstOutput)
		if activeMissTracker != nil {
			cmd = cmd.WithStderrWriter(activeMissTracker)
		}
	} else {
		cmd = cmd.WithQuiet()
	}
	if !cgoEnabled {
		cmd = cmd.WithEnv("CGO_ENABLED", "0")
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
		// Whatever the compiler managed to write under the temp spelling dies
		// with the failed build. The target file itself was never written, so
		// it stays exactly the way clearBuildOutputs left it: absent.
		build.DiscardOutput(job.outputPath)
		return err
	}
	// Only now do the outputs take the target's name — build/<name> appears
	// whole, or the build fails and nothing is left behind.
	return build.CommitOutput(job.outputPath)
}
