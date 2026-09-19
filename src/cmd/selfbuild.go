package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/go-toolchain/src/build"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// apeAppendEnv names the file the fork's linker appends past an APE's load
// span, which is how a go binary carries its standard library.
const apeAppendEnv = "GOCOSMOAPPEND"

// selfBuildPasses is the most passes a self-build makes.
const selfBuildPasses = 3

// fixedPointSelf is the binary this run proved reproduces itself, or empty.
var fixedPointSelf string

// buildSelf builds this pipeline's own binary: the fork checkout is the
// GOROOT, the standard library it compiles is embedded in the result, and
// the build repeats until a binary reproduces itself.
func buildSelf(r runner.CommandRunner, job buildJob, onFirstOutput func()) error {
	work, err := os.MkdirTemp(argListTempDir(hostos.GOOS()), "go-toolchain-self-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)

	last := fixedPointSelf
	if last == "" {
		if last, err = buildSelfPasses(r, job, work, onFirstOutput); err != nil {
			return err
		}
	}
	if err := copyFile(last, build.TempOutputPath(job.outputPath)); err != nil {
		return err
	}
	if err := os.Chmod(build.TempOutputPath(job.outputPath), 0o755); err != nil {
		return err
	}
	return build.CommitOutput(job.outputPath)
}

// buildSelfPasses runs passes under work, each with the binary the last
// pass built, and answers the pass output that matches its own builder.
func buildSelfPasses(r runner.CommandRunner, job buildJob, work string, onFirstOutput func()) (string, error) {
	goCmd := job.goCmd
	for pass := 1; pass <= selfBuildPasses; pass++ {
		out, err := buildSelfPass(r, job, goCmd, work, pass, onFirstOutput)
		if err != nil {
			return "", fmt.Errorf("pass %d of the self-hosted build: %w", pass, err)
		}
		same, err := sameFile(goCmd[0], out)
		if err != nil {
			return "", err
		}
		if same {
			return out, nil
		}
		goCmd = []string{out, "go"}
		onFirstOutput = nil
	}
	return "", fmt.Errorf("the self-hosted build reached no fixed point in %d passes", selfBuildPasses)
}

// sameFile reports whether the files hold the same bytes.
func sameFile(first, second string) (bool, error) {
	sumFirst, err := fileHash(first)
	if err != nil {
		return false, err
	}
	sumSecond, err := fileHash(second)
	if err != nil {
		return false, err
	}
	return sumFirst == sumSecond, nil
}

// buildSelfPass writes the standard library blob with goCmd, then builds
// the tree with goCmd and that blob appended, into a directory of its own.
func buildSelfPass(r runner.CommandRunner, job buildJob, goCmd []string, work string, pass int, onFirstOutput func()) (string, error) {
	dir := filepath.Join(work, fmt.Sprintf("pass%d", pass))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	blob := filepath.Join(dir, "std.blob")
	if err := writeStdBlob(r, goCmd, job.goroot, blob); err != nil {
		return "", err
	}
	passJob := job
	passJob.goCmd = goCmd
	passJob.apeAppend = blob
	passJob.outputPath = filepath.Join(dir, filepath.Base(job.outputPath))
	passJob.selfHosted = false
	passJob.ldflags = joinLDFlags(passJob.ldflags, "-X "+forkCommitVar+"="+resolvedForkCommit)
	if err := runBuild(r, passJob, onFirstOutput); err != nil {
		return "", err
	}
	logger.Info("  pass %d: %s, standard library %s", pass, fileSizeText(passJob.outputPath), fileSizeText(blob))
	return passJob.outputPath, nil
}

// writeStdBlob runs the go command's embedstd tool, which compiles the
// standard library for every architecture the APE carries and writes them
// to blob.
func writeStdBlob(r runner.CommandRunner, goCmd []string, goroot, blob string) error {
	args := append(append([]string{}, goCmd[1:]...), "tool", "embedstd", "-o", blob)
	for _, word := range goCmd {
		args = append(args, "-go", word)
	}
	cmd := runner.Cmd(goCmd[0], args...).
		WithEnv("GOTOOLCHAIN", "local").
		WithEnv("GOROOT", goroot).
		WithQuiet()
	proc, err := cmd.Run(r)
	if err != nil {
		return fmt.Errorf("embedding the standard library: %w", err)
	}
	io.Copy(io.Discard, proc.Stdout())
	stderr, _ := io.ReadAll(proc.Stderr())
	if err := proc.Wait(); err != nil {
		return fmt.Errorf("embedding the standard library: %w\n%s", err, bytes.TrimSpace(stderr))
	}
	if _, err := os.Stat(blob); err != nil {
		return fmt.Errorf("embedstd reported success and wrote no blob at %s", blob)
	}
	return nil
}

// fileSizeText renders a file's size in megabytes, for a log line.
func fileSizeText(path string) string {
	info, err := os.Stat(path)
	if err != nil {
		return "size unknown"
	}
	return sizeText(info.Size())
}

// sizeText renders a byte count in megabytes.
func sizeText(size int64) string {
	return fmt.Sprintf("%.1f MB", float64(size)/(1<<20))
}
