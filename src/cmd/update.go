package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	semver "github.com/Masterminds/semver/v3"
	"github.com/spf13/cobra"
	selfupdate "github.com/wow-look-at-my/go-selfupdate-mini"
)

const npmRegistryBase = "https://git.pazer.us/api/packages/wow-look-at-my/npm/"

// parseVersion parses a version string using strict semver (MAJOR.MINOR.PATCH),
// stripping an optional leading "v". Unlike [semver.NewVersion], this rejects
// bare numbers like "0648669" (a git short SHA composed entirely of digits),
// which NewVersion would otherwise accept as major=648669.
func parseVersion(s string) (*semver.Version, error) {
	return semver.StrictNewVersion(strings.TrimPrefix(s, "v"))
}

// selfUpdater abstracts the self-update mechanism for testability.
type selfUpdater interface {
	detect(ctx context.Context, slug string) (version string, found bool, err error)
	isNewer(currentVersion string) bool
	applyUpdate(ctx context.Context, exePath string) error
}

// npmUpdater uses go-selfupdate-mini with an NpmSource backend.
type npmUpdater struct {
	updater      *selfupdate.Updater
	latest       *selfupdate.Release
	registryBase string // overrides npmRegistryBase; used in tests
}

func (n *npmUpdater) effectiveBase() string {
	if n.registryBase != "" {
		return n.registryBase
	}
	return npmRegistryBase
}

func (n *npmUpdater) detect(ctx context.Context, _ string) (string, bool, error) {
	source, err := selfupdate.NewNpmSource(selfupdate.NpmConfig{
		Registry: n.effectiveBase(),
		Token:    os.Getenv("GITEA_NPM_TOKEN"),
	})
	if err != nil {
		return "", false, err
	}
	updater, err := selfupdate.NewUpdater(selfupdate.Config{Source: source})
	if err != nil {
		return "", false, err
	}
	n.updater = updater

	repo, err := selfupdate.NpmRepositoryFromBuildInfo()
	if err != nil {
		return "", false, fmt.Errorf("npm auto-detect package: %w", err)
	}
	rel, found, err := updater.DetectLatest(ctx, repo)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	n.latest = rel
	return rel.Version.Original, true, nil
}

func (n *npmUpdater) isNewer(currentVersion string) bool {
	if n.latest == nil {
		return false
	}
	cur, err := parseVersion(currentVersion)
	if err != nil {
		return true
	}
	latest, err := parseVersion(n.latest.Version.Version)
	if err != nil {
		return false
	}
	return latest.GreaterThan(cur)
}

func (n *npmUpdater) applyUpdate(ctx context.Context, exePath string) error {
	return n.updater.UpdateTo(ctx, n.latest, exePath)
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

	latestVersion, found, err := u.detect(ctx, "wow-look-at-my/go-toolchain")
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
