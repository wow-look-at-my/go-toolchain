package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
)

// Test seams, so a test drives the go command probe without a toolchain.
var (
	goCacheDirFunc        = goCacheDir
	verifyGoToolchainFunc = verifyGoToolchain
)

// resolvedGoMinor caches the resolved Go minor version so goSupportsFeature avoids re-running "go version".
var resolvedGoMinor int

// activeGoVersion is the full version the go command reports, spelled as runtime.Version spells it.
var activeGoVersion string

// forkSatisfiesGoMod fails when go.mod asks for a newer Go than the fork
// carries. There is no other toolchain to fall back to, so this names the
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
		return fmt.Errorf("go.mod requires Go %s but the gosmopolitan toolchain is %s: update the fork or lower the go directive", required, installed)
	}
	return nil
}

// goVersionCore keeps the leading numeric part, so the fork's own stamp
// compares as the plain release it is built from.
func goVersionCore(v string) string {
	for i, r := range v {
		if (r < '0' || r > '9') && r != '.' {
			return strings.TrimSuffix(v[:i], ".")
		}
	}
	return v
}

// verifyGoToolchain loads "runtime" via goPath, catching a GOROOT that runs but cannot compile.
// GOTOOLCHAIN=local, an emptied GOFLAGS and a go.mod-free directory keep a downloaded toolchain
// and the caller's own -race out of the answer.
func verifyGoToolchain(goPath string) error {
	cmd := exec.Command(goPath, "list", "runtime")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=")
	cmd.Dir = os.TempDir()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// recordGoMinor parses a dotted version string and stores its minor
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
	fallback.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=")
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
	// Output format: "go version goX.Y.Z <goos>/<goarch>"
	fields := strings.Fields(string(out))
	if len(fields) < 3 || !strings.HasPrefix(fields[2], "go") {
		return "", fmt.Errorf("unexpected go version output: %s", out)
	}
	return strings.TrimPrefix(fields[2], "go"), nil
}

// requiredGoVersion reads the go.mod file and returns the Go version needed.
// It prefers the "toolchain goX.Y.Z" directive (if present) over the "go X.Y.Z"
// directive, since the toolchain directive specifies the exact version to use.
// A release archive is named for its patch version, so a go.mod naming only
// major and minor is normalized to carry an explicit patch component.
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
			// "toolchain goX.Y.Z" -> "X.Y.Z"
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

// normalizeGoVersion appends a missing patch component: a release archive is named
// "goX.Y.Z", so a bare "X.Y" must gain a patch component for the download URL.
func normalizeGoVersion(v string) string {
	parts := strings.Split(v, ".")
	if len(parts) == 2 {
		// Only major and minor — append the patch component
		return v + ".0"
	}
	return v
}

func goCacheDir() (string, error) {
	dir, err := userCacheRoot()
	if err != nil {
		return "", err
	}
	cacheDir := filepath.Join(dir, "go-toolchain")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", err
	}
	return cacheDir, nil
}
