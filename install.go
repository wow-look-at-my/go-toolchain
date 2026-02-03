package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var (
	installCopy bool
	installTo   string
)

func init() {
	installCmd := &cobra.Command{
		Use:          "install",
		Short:        "Build and install the binary",
		Long:         "Builds the project and installs the binary via symlink (default) or copy.",
		SilenceUsage: true,
		RunE:         runInstall,
	}
	installCmd.Flags().BoolVar(&installCopy, "copy", false, "Copy instead of symlink")
	installCmd.Flags().StringVar(&installTo, "to", "", "Target directory (default: ~/.local/bin)")
	installCmd.Flags().StringVar(&covDetail, "cov-detail", "", "Show detailed coverage: 'func' or 'file'")
	installCmd.Flags().Float32Var(&minCoverage, "min-coverage", 80.0, "Minimum coverage percentage")
	installCmd.Flags().StringVarP(&output, "output", "o", "", "Output binary name (default: directory name)")
	installCmd.Flags().BoolVar(&jsonOutput, "json", false, "Output coverage report as JSON")

	rootCmd.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) error {
	runner := &RealCommandRunner{Quiet: jsonOutput}
	return runInstallWithRunner(runner)
}

func runInstallWithRunner(runner CommandRunner) error {
	// Run the build first
	if err := runWithRunner(runner); err != nil {
		return err
	}

	// Determine target directory
	targetDir := installTo
	if targetDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		targetDir = filepath.Join(home, ".local", "bin")
	}

	// Ensure target directory exists
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Get absolute path to built binary
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get working directory: %w", err)
	}

	binaryName := output
	if binaryName == "" {
		binaryName = filepath.Base(wd)
	}

	sourcePath := filepath.Join(wd, binaryName)
	targetPath := filepath.Join(targetDir, binaryName)

	// Remove existing file/symlink if present
	if _, err := os.Lstat(targetPath); err == nil {
		if err := os.Remove(targetPath); err != nil {
			return fmt.Errorf("failed to remove existing %s: %w", targetPath, err)
		}
	}

	if installCopy {
		if err := copyFile(sourcePath, targetPath); err != nil {
			return fmt.Errorf("failed to copy binary: %w", err)
		}
		if !jsonOutput {
			fmt.Printf("==> Copied %s to %s\n", binaryName, targetPath)
		}
	} else {
		if err := os.Symlink(sourcePath, targetPath); err != nil {
			return fmt.Errorf("failed to create symlink: %w", err)
		}
		if !jsonOutput {
			fmt.Printf("==> Symlinked %s -> %s\n", targetPath, sourcePath)
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	sourceInfo, err := sourceFile.Stat()
	if err != nil {
		return err
	}

	destFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, sourceInfo.Mode())
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}
