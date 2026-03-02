package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/creativeprojects/go-selfupdate"
	"github.com/spf13/cobra"
)

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
	ctx := context.Background()
	updater := selfupdate.DefaultUpdater()
	repo := selfupdate.ParseSlug("wow-look-at-my/go-toolchain")

	fmt.Println("==> Checking for updates...")

	latest, found, err := updater.DetectLatest(ctx, repo)
	if err != nil {
		return fmt.Errorf("failed to detect latest release: %w", err)
	}
	if !found {
		return fmt.Errorf("no release found for this platform")
	}

	fmt.Printf("    Latest:  %s\n", latest.Version())
	fmt.Printf("    Current: %s\n", buildVersion)

	if buildVersion == "dev" {
		fmt.Println("    Warning: this is a dev build (no embedded version)")
	} else if !latest.GreaterThan(buildVersion) {
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

	if err := updater.UpdateTo(ctx, latest, exePath); err != nil {
		return fmt.Errorf("update failed: %w", err)
	}

	fmt.Printf("==> Updated to %s\n", latest.Version())

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
