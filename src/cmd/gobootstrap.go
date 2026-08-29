package cmd

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/wow-look-at-my/go-toolchain/src/logger"
)

// Test seams — overridden in tests to avoid real downloads / corrupted runners.
var (
	goCacheDirFunc        = goCacheDir
	verifyGoToolchainFunc = verifyGoToolchain
)

// resolvedGoMinor caches the resolved Go minor version so goSupportsFeature avoids re-running "go version".
var resolvedGoMinor int

// EnsureGoVersion puts the gosmopolitan toolchain on PATH and GOROOT, so every
// phase after it -- tidy, vet, test, bench, build -- compiles with the fork and
// nothing else. Whatever Go the host happens to carry is ignored: it lacks the
// org's fixes, and its wasm support is unmaintained.
//
// The fork's own default GOOS is cosmo, which is why the build phase emits the
// fat APE without asking for it, and why a per-platform native binary has no
// compiler left to come out of.
//
// Call this early in main, before any cobra/build logic runs.
func EnsureGoVersion() error {
	goRoot, err := ensureCosmoToolchainFunc()
	if err != nil {
		return fmt.Errorf("the gosmopolitan toolchain is the only compiler this pipeline uses: %w", err)
	}
	useForkAsPipelineToolchain(goRoot)

	goPath, err := exec.LookPath("go")
	if err != nil {
		return fmt.Errorf("gosmopolitan toolchain at %s is not on PATH after setup: %w", goRoot, err)
	}
	if err := verifyGoToolchainFunc(goPath); err != nil {
		return fmt.Errorf("gosmopolitan toolchain at %s failed its integrity probe: %w", goRoot, err)
	}

	installed, err := installedGoVersion()
	if err != nil {
		return fmt.Errorf("gosmopolitan toolchain at %s will not report its version: %w", goRoot, err)
	}
	if err := forkSatisfiesGoMod(installed); err != nil {
		return err
	}
	recordGoMinor(goVersionCore(installed))
	logger.Info("go-bootstrap: using the gosmopolitan toolchain %s from %s", installed, goRoot)
	return nil
}

// useForkAsPipelineToolchain points this process and everything it spawns at
// goRoot. GOTOOLCHAIN=local is the half that makes it stick: without it the go
// command downloads a stock toolchain to satisfy a go.mod directive.
func useForkAsPipelineToolchain(goRoot string) {
	os.Setenv("PATH", filepath.Join(goRoot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	os.Setenv("GOROOT", goRoot)
	os.Setenv("GOTOOLCHAIN", "local")
}

// forkSatisfiesGoMod fails when go.mod asks for a newer Go than the fork
// carries. There is no second toolchain to fall back to, so this names the
// repair -- a newer fork -- rather than reaching for a stock Go.
func forkSatisfiesGoMod(installed string) error {
	required, err := requiredGoVersion()
	if err != nil || required == "" {
		return nil // no readable go.mod: nothing to satisfy
	}
	installedVer, err := semver.NewVersion(goVersionCore(installed))
	if err != nil {
		return nil // unparseable fork version: the integrity probe already passed, so build
	}
	requiredVer, err := semver.NewVersion(required)
	if err != nil {
		return nil
	}
	if installedVer.LessThan(requiredVer) {
		return fmt.Errorf("go.mod requires Go %s but the gosmopolitan toolchain is %s: update the fork (%s selects its branch) or lower the go directive", required, installed, cosmoBranchEnv)
	}
	return nil
}

// goVersionCore keeps the leading numeric part of a version, so the fork's own
// "1.27.0cosmo.r685" compares as 1.27.0.
func goVersionCore(v string) string {
	for i, r := range v {
		if (r < '0' || r > '9') && r != '.' {
			return strings.TrimSuffix(v[:i], ".")
		}
	}
	return v
}

// verifyGoToolchain loads the "runtime" package via goPath to catch a half-extracted GOROOT that
// runs and reports a version but cannot compile. It sets GOTOOLCHAIN=local in a directory with no
// go.mod, so neither an auto-downloaded toolchain nor a module directive can skew the result.
func verifyGoToolchain(goPath string) error {
	cmd := exec.Command(goPath, "list", "runtime")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	cmd.Dir = os.TempDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// recordGoMinor parses a version string like "1.24.7" and stores its minor
// component in resolvedGoMinor so that goSupportsFeature can use it without
// shelling out to "go version". If ver is empty or unparseable, it falls back
// to running "go version".
func recordGoMinor(ver string) {
	parts := strings.SplitN(ver, ".", 3)
	if len(parts) >= 2 {
		if minor, err := strconv.Atoi(parts[1]); err == nil {
			resolvedGoMinor = minor
			return
		}
	}
	// Fallback when ver is unparseable: run "go version" with GOTOOLCHAIN=local for the real version.
	fallback := exec.Command("go", "version")
	fallback.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := fallback.Output()
	if err != nil {
		return
	}
	fields := strings.Fields(string(out))
	if len(fields) < 3 {
		return
	}
	v := strings.TrimPrefix(fields[2], "go")
	parts = strings.SplitN(v, ".", 3)
	if len(parts) >= 2 {
		if minor, err := strconv.Atoi(parts[1]); err == nil {
			resolvedGoMinor = minor
		}
	}
}

// installedGoVersion runs "go version" and extracts the version number.
// It forces GOTOOLCHAIN=local so that Go reports the real installed version
// rather than auto-downloading a stripped toolchain module that inflates the
// reported version.
func installedGoVersion() (string, error) {
	cmd := exec.Command("go", "version")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// Output format: "go version go1.24.11 linux/amd64"
	fields := strings.Fields(string(out))
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "go") {
		return "", fmt.Errorf("unexpected go version output: %s", out)
	}
	return strings.TrimPrefix(fields[2], "go"), nil
}

// requiredGoVersion reads the go.mod file and returns the Go version needed.
// It prefers the "toolchain goX.Y.Z" directive (if present) over the "go X.Y.Z"
// directive, since the toolchain directive specifies the exact version to use.
// Since Go 1.21, release archives include the patch version (e.g. go1.25.0),
// so if go.mod says "go 1.25" we normalize it to "1.25.0".
func requiredGoVersion() (string, error) {
	f, err := os.Open("go.mod")
	if err != nil {
		return "", err
	}
	defer f.Close()

	var goVer, toolchainVer string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "toolchain ") {
			tc := strings.TrimSpace(strings.TrimPrefix(line, "toolchain "))
			// "toolchain go1.25.0" -> "1.25.0"
			toolchainVer = strings.TrimPrefix(tc, "go")
		} else if goVer == "" && strings.HasPrefix(line, "go ") {
			goVer = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}

	// Prefer the toolchain directive — it's more specific.
	if toolchainVer != "" {
		return normalizeGoVersion(toolchainVer), nil
	}
	return normalizeGoVersion(goVer), nil
}

// normalizeGoVersion appends a missing patch component: since Go 1.21 release archives are named
// go1.X.0, so "1.25" must become "1.25.0" for the download URL.
func normalizeGoVersion(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) == 2 {
		// Only major.minor (e.g. "1.25") — append ".0"
		return v + ".0"
	}
	return v
}

func goCacheDir() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		// Fallback for systems without XDG
		home, err2 := os.UserHomeDir()
		if err2 != nil {
			return "", fmt.Errorf("cannot determine cache directory: %w", err)
		}
		dir = filepath.Join(home, ".cache")
	}
	cacheDir := filepath.Join(dir, "go-toolchain")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}
	return cacheDir, nil
}

func extractTarGz(r io.Reader, destDir string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		target := filepath.Join(destDir, hdr.Name)

		// Guard against path traversal
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(destDir)+string(os.PathSeparator)) {
			continue
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
	return nil
}
