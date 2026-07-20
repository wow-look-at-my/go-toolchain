package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/profile"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

func createHostSymlinks(targets []build.Target, outDir string) error {
	// hostos, not runtime: the symlink must point at the matrix binary built
	// for the OS this process is running on, and a cosmo fat APE reports
	// runtime.GOOS=="cosmo" everywhere. runtime.GOARCH matches the host.
	hostOS := hostos.GOOS()
	hostArch := runtime.GOARCH

	for _, target := range targets {
		hostBinary := build.BinaryName(target.OutputName, hostOS, hostArch)
		ext := ""
		if hostOS == "windows" {
			ext = ".exe"
		}

		// Verify the host binary exists in the output directory
		hostPath := filepath.Join(outDir, hostBinary)
		if _, err := os.Stat(hostPath); err != nil {
			logger.Info("  SKIP symlink for %s (host binary %s not found)", target.OutputName, hostBinary)
			continue
		}

		// Create <name>_host and <name> symlinks (relative, pointing to the host binary)
		for _, suffix := range []string{"_host", ""} {
			linkName := target.OutputName + suffix + ext
			linkPath := filepath.Join(outDir, linkName)
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
		// CGO_ENABLED=0 always: cosmopolitan has no cgo.
		cmd = cmd.WithEnv("GOOS", cosmoOS).
			WithEnv("GOARCH", "").
			WithEnv("GOCOSMOFAT", "").
			WithEnv("GOTOOLCHAIN", "local").
			WithEnv("GOROOT", job.forkGoroot).
			WithEnv("PATH", filepath.Join(job.forkGoroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH")).
			WithEnv("CGO_ENABLED", "0")
	case job.forkGoroot != "":
		// Wasm build (js/wasm or wasip1/wasm) via the gosmopolitan toolchain.
		// The fork DEFAULTS to GOOS=cosmo, so GOOS and GOARCH are always
		// pinned explicitly. CGO_ENABLED=0 always: wasm has no cgo.
		cmd = cmd.WithEnv("GOOS", job.goos).
			WithEnv("GOARCH", job.goarch).
			WithEnv("GOTOOLCHAIN", "local").
			WithEnv("GOROOT", job.forkGoroot).
			WithEnv("PATH", filepath.Join(job.forkGoroot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH")).
			WithEnv("CGO_ENABLED", "0")
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
