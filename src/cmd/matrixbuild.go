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

// hostRunnableArtifact names the APE, which runs here by construction. Named
// even when absent, so callers report a missing artifact instead of a wrong path.
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

// checkPortableJob allows only the fat APE and wasm, only through the fork.
// It sits at the sole chokepoint that compiles anything, so a call site that
// invents a target fails here instead of shipping a native binary.
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
	// A call site that forgets to fingerprint the toolchain fails loudly rather
	// than reopening cross-toolchain cache poisoning.
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
	forkGoBin := cosmoGoBinPath(job.forkGoroot)
	// An ambient GOOS is the last way to ask for a native binary, so every variable below is assigned. No output has cgo.
	cmd := runner.Cmd(forkGoBin, args...).
		WithEnv("GOTOOLCHAIN", "local").
		WithEnv("GOROOT", job.forkGoroot).
		WithEnv("PATH", filepath.Join(job.forkGoroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH")).
		WithEnv("CGO_ENABLED", "0").
		WithEnv(cache.KeyNamespaceEnv, job.cacheNamespace)
	if job.goos == cosmoOS {
		// "fat" is a pseudo-arch, and an inherited GOCOSMOFAT would silently
		// produce a thin binary, so each is cleared.
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
