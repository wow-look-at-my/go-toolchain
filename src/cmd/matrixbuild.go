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

// hostRunnableArtifact returns the artifact in outDir that runs on this host:
// the native <name>_<hostos>_<hostarch> build when one exists, else the fat
// APE, which runs here by construction. Without the fallback, a default matrix
// run — one APE and no per-platform copies — would leave the dats phase and
// the convenience symlinks with nothing to point at. The path is returned even
// when neither exists, so callers report a missing artifact rather than a
// wrong one.
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
	// hostos, not runtime: the symlink must point at the matrix binary built
	// for the OS this process is running on, and a cosmo fat APE reports
	// runtime.GOOS=="cosmo" everywhere. runtime.GOARCH matches the host.
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
			// A cosmo build writes the APE under the plain name, so that name
			// is a real binary and not a link slot. Overwriting it deletes the
			// artifact -- and in a cosmo+native build the APE is not even the
			// host binary, so the link would look correct while the APE was
			// gone.
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
func runBuild(r runner.CommandRunner, job buildJob, onFirstOutput func()) error {
	// Last-chokepoint guard: a fork-toolchain job MUST carry a cache
	// namespace (see buildJob.cacheNamespace). Refusing to build here means a
	// future call site that forgets to fingerprint the toolchain fails loudly
	// instead of silently re-opening cross-toolchain cache poisoning.
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
	args = append(args, "-o", job.outputPath, job.srcPath)
	goCmd := "go"
	if job.forkGoroot != "" {
		goCmd = filepath.Join(job.forkGoroot, "bin", "go")
	}
	cmd := runner.Cmd(goCmd, args...)
	switch {
	case job.forkGoroot != "" && job.goos == cosmoOS:
		// GOOS=cosmo fat-APE build via the gosmopolitan toolchain. GOARCH is
		// cleared: fat (amd64+arm64+windows payloads in one output) is the
		// fork's default and the job's pseudo-arch "fat" is a naming artifact,
		// not a GOARCH. GOCOSMOFAT is cleared too so an inherited =0 cannot
		// silently produce a thin binary that the slot copies would mislabel.
		// CGO_ENABLED=0 always: cosmopolitan has no cgo. The cache namespace
		// keys this build's cacheprog to THIS toolchain's content (its
		// cacheprog then skips the shared daemon and namespaces every key —
		// see cache.KeyNamespaceEnv), because the fork's constant version
		// stamp would otherwise collide its action IDs with every other fork
		// toolchain build's.
		// GOCOSMOPLATFORMS names the host platforms the APE must cover, so the
		// fork skips building and merging the payloads nothing in the set
		// needs. Always assigned, never left inherited: an ambient value would
		// silently change which platforms the artifact claims to run on.
		cmd = cmd.WithEnv("GOOS", cosmoOS).
			WithEnv("GOARCH", "").
			WithEnv("GOCOSMOFAT", "").
			WithEnv(cosmoPlatformsEnv, job.cosmoPlatforms).
			WithEnv("GOTOOLCHAIN", "local").
			WithEnv("GOROOT", job.forkGoroot).
			WithEnv("PATH", filepath.Join(job.forkGoroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH")).
			WithEnv("CGO_ENABLED", "0").
			WithEnv(cache.KeyNamespaceEnv, job.cacheNamespace)
		// GOWORK redirects one or more third-party modules with no cosmo port
		// to cosmocompat's patched copies (see cosmocompat.Prepare); empty for
		// a consumer that depends on none of them, so GOWORK stays unset and
		// this build resolves exactly as it always has.
		if job.goWork != "" {
			cmd = cmd.WithEnv("GOWORK", job.goWork)
		}
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
	if err != nil {
		return err
	}
	if onFirstOutput != nil {
		// Non-quiet: let Wait() stream -v output to console in real-time.
		// Compiler errors are printed to stderr as they occur.
		return proc.Wait()
	}
	// Quiet (matrix): drain pipes manually, capture stderr for error messages
	io.Copy(io.Discard, proc.Stdout())
	stderr, _ := io.ReadAll(proc.Stderr())
	if err := proc.Wait(); err != nil {
		if len(stderr) > 0 {
			return fmt.Errorf("%w\n%s", err, stderr)
		}
		return err
	}
	return nil
}
