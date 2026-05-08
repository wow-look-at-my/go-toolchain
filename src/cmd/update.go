package cmd

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	semver "github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
)

const (
	npmRegistryBase = "https://git.pazer.us/api/packages/wow-look-at-my/npm/"
	npmScope        = "@wow-look-at-my"
	npmPkgName      = "go-toolchain"
)

// parseVersion parses a version string using strict semver (MAJOR.MINOR.PATCH),
// stripping an optional leading "v". Unlike [semver.NewVersion], this rejects
// bare numbers like "0648669" (a git short SHA composed entirely of digits),
// which NewVersion would otherwise accept as major=648669.
func parseVersion(s string) (*semver.Version, error) {
	return semver.StrictNewVersion(strings.TrimPrefix(s, "v"))
}

// selfUpdater abstracts the self-update mechanism for testability.
type selfUpdater interface {
	detect(ctx context.Context) (version string, found bool, err error)
	isNewer(currentVersion string) bool
	applyUpdate(ctx context.Context, exePath string) error
}

// npmOS maps runtime.GOOS to the npm platform string used in package names.
func npmOS() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	default:
		return runtime.GOOS
	}
}

// npmArch maps runtime.GOARCH to the npm cpu string used in package names.
func npmArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	default:
		return runtime.GOARCH
	}
}

// npmUpdater downloads the latest binary from the private npm registry.
type npmUpdater struct {
	latestVersion string
	tarballURL    string
	registryBase  string // overrides npmRegistryBase; used in tests
}

func (n *npmUpdater) effectiveBase() string {
	if n.registryBase != "" {
		return n.registryBase
	}
	return npmRegistryBase
}

func (n *npmUpdater) detect(ctx context.Context) (string, bool, error) {
	token := os.Getenv("GITEA_NPM_TOKEN")
	base := n.effectiveBase()

	// Fetch wrapper package metadata to find the latest version.
	metaURL := base + npmScope + "/" + npmPkgName
	req, err := http.NewRequestWithContext(ctx, "GET", metaURL, nil)
	if err != nil {
		return "", false, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("npm registry request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("npm registry returned HTTP %d", resp.StatusCode)
	}

	var meta struct {
		DistTags map[string]string `json:"dist-tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		return "", false, fmt.Errorf("failed to parse npm registry response: %w", err)
	}

	latest := meta.DistTags["latest"]
	if latest == "" {
		return "", false, nil
	}
	n.latestVersion = latest

	// Fetch the platform-specific package metadata to get the tarball URL.
	platPkg := npmPkgName + "-" + npmOS() + "-" + npmArch()
	platMetaURL := base + npmScope + "/" + platPkg
	platReq, err := http.NewRequestWithContext(ctx, "GET", platMetaURL, nil)
	if err != nil {
		return "", false, err
	}
	if token != "" {
		platReq.Header.Set("Authorization", "Bearer "+token)
	}

	platResp, err := client.Do(platReq)
	if err != nil {
		return "", false, fmt.Errorf("npm platform package request failed: %w", err)
	}
	defer platResp.Body.Close()

	if platResp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("npm platform package returned HTTP %d", platResp.StatusCode)
	}

	var platMeta struct {
		Versions map[string]struct {
			Dist struct {
				Tarball string `json:"tarball"`
			} `json:"dist"`
		} `json:"versions"`
	}
	if err := json.NewDecoder(platResp.Body).Decode(&platMeta); err != nil {
		return "", false, fmt.Errorf("failed to parse platform package metadata: %w", err)
	}

	ver := strings.TrimPrefix(latest, "v")
	if vd, ok := platMeta.Versions[ver]; ok {
		n.tarballURL = vd.Dist.Tarball
	}
	if n.tarballURL == "" {
		return "", false, fmt.Errorf("no tarball found for %s@%s", platPkg, ver)
	}

	return latest, true, nil
}

func (n *npmUpdater) isNewer(currentVersion string) bool {
	cur, err := parseVersion(currentVersion)
	if err != nil {
		return true
	}
	latest, err := parseVersion(n.latestVersion)
	if err != nil {
		return false
	}
	return latest.GreaterThan(cur)
}

func (n *npmUpdater) applyUpdate(ctx context.Context, exePath string) error {
	if n.tarballURL == "" {
		return fmt.Errorf("applyUpdate called before detect: no tarball URL available")
	}
	token := os.Getenv("GITEA_NPM_TOKEN")

	req, err := http.NewRequestWithContext(ctx, "GET", n.tarballURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download update: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}

	binData, err := extractBinaryFromTarGz(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to extract binary: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(exePath), ".go-toolchain-update-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(binData); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to write update: %w", err)
	}
	if err := tmp.Chmod(0755); err != nil {
		tmp.Close()
		return fmt.Errorf("failed to chmod update: %w", err)
	}
	tmp.Close()

	if err := os.Rename(tmpName, exePath); err != nil {
		return fmt.Errorf("failed to replace binary: %w", err)
	}
	return nil
}

// extractBinaryFromTarGz reads a .tgz and returns the contents of
// package/bin/go-toolchain (or .exe on Windows).
func extractBinaryFromTarGz(r io.Reader) ([]byte, error) {
	binName := npmPkgName
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	target := "package/bin/" + binName

	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decompress: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read archive: %w", err)
		}
		if hdr.Name == target {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("binary %q not found in archive", target)
}

// newUpdater is the factory for creating updaters. Replaceable for testing.
var newUpdater = func() selfUpdater { return &npmUpdater{} }

func init() {
	updateCmd := &cobra.Command{
		Use:          "update",
		Short:        "Update go-toolchain to the latest release",
		SilenceUsage: true,
		RunE:         runUpdate,
	}
	rootCmd.AddCommand(updateCmd)
}

func runUpdate(cmd *cobra.Command, args []string) error {
	return doUpdate(context.Background(), newUpdater())
}

func doUpdate(ctx context.Context, u selfUpdater) error {
	fmt.Println("⇒ Checking for updates...")

	latestVersion, found, err := u.detect(ctx)
	if err != nil {
		return fmt.Errorf("failed to detect latest release: %w", err)
	}
	if !found {
		return fmt.Errorf("no release found for this platform")
	}

	fmt.Printf("    Latest:  %s\n", latestVersion)
	fmt.Printf("    Current: %s\n", buildVersion)

	if buildVersion == "dev" {
		fmt.Println("    Warning: this is a dev build (no embedded version)")
	} else if _, err := parseVersion(buildVersion); err != nil {
		fmt.Printf("    Warning: current version %q is not valid semver, proceeding with update\n", buildVersion)
	} else if !u.isNewer(buildVersion) {
		fmt.Println("⇒ Already up to date.")
		return nil
	}

	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find current executable: %w", err)
	}
	exePath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	fmt.Printf("⇒ Updating %s ...\n", exePath)

	if err := u.applyUpdate(ctx, exePath); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("⇒ Updated to %s\n", latestVersion)

	// Re-create go-safe-build compat symlink if binary is in ~/.local/bin/
	home, err := os.UserHomeDir()
	if err != nil {
		return nil // non-fatal
	}
	localBin := filepath.Join(home, ".local", "bin")
	if filepath.Dir(exePath) == localBin {
		compatPath := filepath.Join(localBin, "go-safe-build")
		if _, err := os.Lstat(compatPath); err == nil {
			os.Remove(compatPath)
		}
		if err := os.Symlink(exePath, compatPath); err != nil {
			fmt.Printf("⇒ Warning: failed to update compat symlink: %v\n", err)
		} else {
			fmt.Printf("⇒ Symlinked %s -> %s\n", compatPath, exePath)
		}
	}

	return nil
}
