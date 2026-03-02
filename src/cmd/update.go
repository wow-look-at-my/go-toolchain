package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/spf13/cobra"
)

// selfUpdater abstracts the self-update mechanism for testability.
type selfUpdater interface {
	detect(ctx context.Context, slug string) (version string, found bool, err error)
	isNewer(currentVersion string) bool
	applyUpdate(ctx context.Context, exePath string) error
}

// githubUpdater wraps go-selfupdate for real GitHub releases.
type githubUpdater struct {
	updater *selfupdate.Updater
	latest  *selfupdate.Release
}

func (g *githubUpdater) detect(ctx context.Context, slug string) (string, bool, error) {
	g.updater = selfupdate.DefaultUpdater()
	repo := selfupdate.ParseSlug(slug)
	rel, found, err := g.updater.DetectLatest(ctx, repo)
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	g.latest = rel
	return rel.Version(), true, nil
}

func (g *githubUpdater) isNewer(currentVersion string) bool {
	if g.latest == nil {
		return false
	}
	return g.latest.GreaterThan(currentVersion)
}

func (g *githubUpdater) applyUpdate(ctx context.Context, exePath string) error {
	return g.updater.UpdateTo(ctx, g.latest, exePath)
}

// newUpdater is the factory for creating updaters. Replaceable for testing.
var newUpdater = func() selfUpdater { return &githubUpdater{} }

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
	fmt.Println("==> Checking for updates...")

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
	} else if !u.isNewer(buildVersion) {
		fmt.Println("==> Already up to date.")
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

	fmt.Printf("==> Updating %s ...\n", exePath)

	if err := u.applyUpdate(ctx, exePath); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("==> Updated to %s\n", latestVersion)

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
			fmt.Printf("==> Warning: failed to update compat symlink: %v\n", err)
		} else {
			fmt.Printf("==> Symlinked %s -> %s\n", compatPath, exePath)
		}
	}

	return nil
}
