// Package codeql wraps the CodeQL Go extractor and analyze CLI invocations,
// emitting them as native go-toolchain pipeline steps so they are captured
// in the timeline and OTLP traces alongside vet/test/build/matrix work.
//
// It assumes github/codeql-action/init has already run and exported the
// CODEQL_DIST and CODEQL_EXTRACTOR_GO_* environment variables. Enabled()
// reports whether that's the case.
package codeql

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/wow-look-at-my/go-toolchain/src/hostos"
	"github.com/wow-look-at-my/go-toolchain/src/runner"
)

// Enabled reports whether the current process was launched in a CodeQL-aware
// context (i.e. github/codeql-action/init has run).
func Enabled() bool {
	return os.Getenv("CODEQL_DIST") != ""
}

// Extract runs the standalone Go extractor against ./..., populating the
// CodeQL database at CODEQL_EXTRACTOR_GO_WIP_DATABASE without a `go build`.
func Extract(r runner.CommandRunner) error {
	bin, err := extractorPath()
	if err != nil {
		return err
	}
	if err := runWait(r, bin, "./..."); err != nil {
		return fmt.Errorf("codeql extract: %w", err)
	}
	return nil
}

// Analyze finalizes the database and runs the security-and-quality query
// suite, writing SARIF to a temp file. Returns the SARIF path.
func Analyze(r runner.CommandRunner) (string, error) {
	db := os.Getenv("CODEQL_EXTRACTOR_GO_WIP_DATABASE")
	if db == "" {
		return "", fmt.Errorf("CODEQL_EXTRACTOR_GO_WIP_DATABASE not set")
	}
	codeql := codeqlBin()

	if err := runWait(r, codeql, "database", "finalize", db); err != nil {
		return "", fmt.Errorf("codeql database finalize: %w", err)
	}

	sarifPath := filepath.Join(os.TempDir(), "go-toolchain-codeql.sarif")
	if err := runWait(r, codeql, "database", "analyze",
		db,
		"--format=sarif-latest",
		"--output="+sarifPath,
		"--sarif-category=/language:go",
		"go-security-and-quality.qls",
	); err != nil {
		return "", fmt.Errorf("codeql database analyze: %w", err)
	}
	return sarifPath, nil
}

// UploadSARIF posts the SARIF file to GitHub's code-scanning API via the
// codeql CLI's `github upload-results` subcommand. Requires GITHUB_TOKEN
// (or GH_TOKEN), GITHUB_SHA, GITHUB_REF, and GITHUB_REPOSITORY in the
// environment. The codeql CLI reads GITHUB_TOKEN from the env directly.
func UploadSARIF(r runner.CommandRunner, sarifPath string) error {
	if os.Getenv("GITHUB_TOKEN") == "" && os.Getenv("GH_TOKEN") == "" {
		return fmt.Errorf("no GITHUB_TOKEN/GH_TOKEN in environment")
	}
	sha := os.Getenv("GITHUB_SHA")
	ref := os.Getenv("GITHUB_REF")
	repo := os.Getenv("GITHUB_REPOSITORY")
	if sha == "" || ref == "" || repo == "" {
		return fmt.Errorf("missing GITHUB_SHA/GITHUB_REF/GITHUB_REPOSITORY")
	}

	cfg := runner.Cmd(codeqlBin(), "github", "upload-results",
		"--sarif="+sarifPath,
		"--commit="+sha,
		"--ref="+ref,
		"--repository="+repo,
	)
	// codeql CLI honors GITHUB_TOKEN — fall back to GH_TOKEN by exporting it.
	if os.Getenv("GITHUB_TOKEN") == "" {
		cfg = cfg.WithEnv("GITHUB_TOKEN", os.Getenv("GH_TOKEN"))
	}
	return runWaitConfig(r, cfg)
}

// extractorPath resolves the platform-specific path to the go-extractor binary
// that ships in the CodeQL bundle.
func extractorPath() (string, error) {
	root := os.Getenv("CODEQL_EXTRACTOR_GO_ROOT")
	if root == "" {
		return "", fmt.Errorf("CODEQL_EXTRACTOR_GO_ROOT not set")
	}
	// hostos, not runtime: the bundle ships host-OS tool dirs, and a cosmo APE reports runtime.GOOS=="cosmo" everywhere.
	return extractorPathFor(root, hostos.GOOS())
}

func extractorPathFor(root, goos string) (string, error) {
	plat, ext, err := platformFor(goos)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "tools", plat, "go-extractor"+ext), nil
}

// codeqlBin returns the path to the codeql CLI driver inside CODEQL_DIST.
func codeqlBin() string {
	return codeqlBinFor(os.Getenv("CODEQL_DIST"), hostos.GOOS())
}

func codeqlBinFor(dist, goos string) string {
	bin := "codeql"
	if goos == "windows" {
		bin += ".exe"
	}
	return filepath.Join(dist, bin)
}

func platformFor(goos string) (plat, ext string, err error) {
	switch goos {
	case "linux":
		return "linux64", "", nil
	case "darwin":
		return "osx64", "", nil
	case "windows":
		return "win64", ".exe", nil
	}
	return "", "", fmt.Errorf("unsupported GOOS for CodeQL: %s", goos)
}

func runWait(r runner.CommandRunner, name string, args ...string) error {
	return runWaitConfig(r, runner.Cmd(name, args...))
}

func runWaitConfig(r runner.CommandRunner, cfg *runner.Config) error {
	proc, err := cfg.Run(r)
	if err != nil {
		return fmt.Errorf("spawn %s: %w", cfg.Name, err)
	}
	// Drain stdout/stderr concurrently: draining either alone can deadlock when the other's OS buffer fills.
	var stderr []byte
	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, proc.Stdout())
		close(done)
	}()
	stderr, _ = io.ReadAll(proc.Stderr())
	<-done

	if err := proc.Wait(); err != nil {
		if len(stderr) > 0 {
			return fmt.Errorf("%w\n%s", err, stderr)
		}
		return err
	}
	return nil
}
