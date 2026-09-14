package cmd

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wow-look-at-my/go-toolchain/src/gomod"
	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// go/parser and go/types link in from whatever built this binary, so the
// pipeline runs only as a build of the active toolchain. Depth: docs/CI.md
const ownModulePath = "github.com/wow-look-at-my/go-toolchain"

// ownRepoURL is where a consumer fetches this binary's own commit to rebuild it.
const ownRepoURL = "https://github.com/wow-look-at-my/go-toolchain"

// Set on the child, so a rebuild that still links another front end stops.
const reexecGuardEnv = "GO_TOOLCHAIN_FORK_REEXEC"

// linkedGoVersion names the toolchain whose standard library this binary links.
func linkedGoVersion() string { return goVersionName(runtime.Version()) }

// The seam: go test builds with the active toolchain, so a test binary always matches it.
var linkedGoVersionFunc = linkedGoVersion

// goVersionName drops the experiment list a version string can carry after a space.
func goVersionName(v string) string {
	if f := strings.Fields(v); len(f) > 0 {
		return f[0]
	}
	return v
}

// reexecUnderActiveToolchain hands this run to a build of the pipeline that
// links the active toolchain's own go/parser and go/types, and exits with that
// build's status. It returns when this binary already links them.
func reexecUnderActiveToolchain(active string) error {
	linked := linkedGoVersionFunc()
	if linked == active {
		return nil
	}
	if os.Getenv(reexecGuardEnv) != "" {
		return fmt.Errorf("the rebuilt pipeline links %s and the active toolchain is %s, so the `go` on PATH did not build it", linked, active)
	}
	pkg, own := ownMainPackage()
	if !own {
		bin, err := pipelineAtRevision(linked, active, getVCS())
		if err != nil {
			return err
		}
		os.Exit(runSelf(bin))
	}
	st := logStep(fmt.Sprintf("rebuilding the pipeline with %s (this binary links %s)", active, linked))
	bin, err := buildSelfWithFork(pkg)
	st.done()
	if err != nil {
		return err
	}
	code := runSelf(bin)
	_ = os.RemoveAll(filepath.Dir(bin))
	os.Exit(code)
	return nil
}

// pipelineAtRevision answers a build of this binary's own commit by the active
// toolchain, cached by that toolchain and that commit, so a host builds it a
// single time per release.
func pipelineAtRevision(linked, active string, vcs vcsInfo) (string, error) {
	if vcs.Revision == "" {
		return "", fmt.Errorf("this pipeline links %s's go/parser and go/types, the active toolchain is %s, and the binary names no commit to rebuild from: install the published binary", linked, active)
	}
	if vcs.Modified {
		return "", fmt.Errorf("this pipeline links %s's go/parser and go/types, the active toolchain is %s, and it was built from a modified tree at %s, which no clone reproduces: rebuild it with `go-toolchain install` from that checkout", linked, active, vcs.Revision)
	}
	root, err := goCacheDirFunc()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(root, "pipeline", pipelineCacheKey(active, vcs.Revision), "go-toolchain"+hostExeSuffix())
	if _, err := os.Stat(bin); err == nil {
		return bin, nil
	}
	// The path enters an argument list, which cosmo does not translate.
	src, err := os.MkdirTemp(argListTempDir(hostos.GOOS()), "go-toolchain-src-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(src) }()
	st := logStep(fmt.Sprintf("fetching go-toolchain %s to rebuild it with %s (this binary links %s)", vcs.Revision, active, linked))
	err = cloneAt(src, ownRepoURL, vcs.Revision)
	st.done()
	if err != nil {
		return "", fmt.Errorf("fetching go-toolchain at %s: %w", vcs.Revision, err)
	}
	if err := buildCheckout(src, bin); err != nil {
		return "", fmt.Errorf("rebuilding go-toolchain %s with %s: %w", vcs.Revision, active, err)
	}
	return bin, nil
}

// pipelineCacheKey names a cached build by the toolchain that built it and the commit it built.
func pipelineCacheKey(active, revision string) string {
	safe := strings.Map(func(r rune) rune {
		if r == '.' || r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, active)
	return safe + "-" + revision
}

// buildCheckout builds the checkout at src into bin. The checkout's own
// dependencies owe their generated output first, as they do in this module.
func buildCheckout(src, bin string) error {
	back, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(src); err != nil {
		return err
	}
	defer func() { _ = os.Chdir(back) }()
	if _, err := satisfyDepGenerate(""); err != nil {
		return err
	}
	pkg, ok := ownMainPackage()
	if !ok {
		return fmt.Errorf("the checkout at %s holds no main package of %s", src, ownModulePath)
	}
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		return err
	}
	// A private directory beside bin, so a concurrent run never sees a partial binary.
	work, err := os.MkdirTemp(filepath.Dir(bin), "build-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(work) }()
	st := logStep("building the fetched pipeline")
	built := filepath.Join(work, filepath.Base(bin))
	err = goBuildHost(pkg, built)
	st.done()
	if err != nil {
		return err
	}
	if err := os.Rename(built, bin); err != nil {
		// A concurrent run can hold the finished binary open, which is the same bytes.
		if _, statErr := os.Stat(bin); statErr == nil {
			return nil
		}
		return err
	}
	return nil
}

// ownMainPackage is false outside this module, where the source comes from a clone.
func ownMainPackage() (string, bool) {
	if gomod.ReadModulePath(".") != ownModulePath {
		return "", false
	}
	mains, err := gomod.FindMainPackagesForTarget(".", hostos.GOOS(), runtime.GOARCH)
	if err != nil || len(mains) != 1 {
		return "", false
	}
	return mains[0], true
}

// buildSelfWithFork compiles the pipeline into a fresh temporary directory.
func buildSelfWithFork(pkg string) (string, error) {
	// The path enters an argument list, which cosmo does not translate.
	dir, err := os.MkdirTemp(argListTempDir(hostos.GOOS()), "go-toolchain-fork-")
	if err != nil {
		return "", err
	}
	bin := filepath.Join(dir, "go-toolchain"+hostExeSuffix())
	if err := goBuildHost(pkg, bin); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return bin, nil
}

// goBuildHost compiles pkg for the HOST with the active toolchain: the fork
// defaults to an APE, and exec does not read a shell header.
func goBuildHost(pkg, bin string) error {
	cmd := exec.Command("go", "build", "-o", bin, pkg)
	cmd.Env = append(os.Environ(),
		"GOTOOLCHAIN=local",
		"GOOS="+hostos.GOOS(),
		"GOARCH="+runtime.GOARCH,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("rebuilding the pipeline with the active toolchain failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runSelf hands this invocation to bin and answers its exit status.
func runSelf(bin string) int { return runSelfWith(bin, reexecGuardEnv) }

func runSelfWith(bin, guard string) int {
	cmd := exec.Command(bin, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = append(os.Environ(), guard+"=1")
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		logger.Error("⇒ the rebuilt pipeline at %s will not run: %v", bin, err)
		return 1
	}
	return 0
}

// hostExeSuffix reads the HOST: NT needs it, and the compile target does not say.
func hostExeSuffix() string {
	if hostos.GOOS() == "windows" {
		return ".exe"
	}
	return ""
}
