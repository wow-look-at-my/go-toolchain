package cmd

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

var installCopy bool

// Register adds all subcommands to the given root command.
func Register(root *cobra.Command) {
	installCmd := &cobra.Command{
		Use:          "install",
		Short:        "Install go-safe-build to ~/.local/bin",
		Long:         "Installs the currently running binary via symlink (default) or copy to ~/.local/bin.",
		SilenceUsage: true,
		RunE:         runInstall,
	}
	installCmd.Flags().BoolVar(&installCopy, "copy", false, "Copy instead of symlink")
	root.AddCommand(installCmd)
}

func runInstall(cmd *cobra.Command, args []string) error {
	return runInstallImpl()
}

func runInstallImpl() error {
	// Source is the currently running binary
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to find current executable: %w", err)
	}
	sourcePath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return fmt.Errorf("failed to resolve executable path: %w", err)
	}

	binaryName := filepath.Base(sourcePath)

	// Target directory is always ~/.local/bin
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}
	targetDir := filepath.Join(home, ".local", "bin")

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

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
		fmt.Printf("==> Copied %s to %s\n", binaryName, targetPath)
	} else {
		if err := os.Symlink(sourcePath, targetPath); err != nil {
			return fmt.Errorf("failed to create symlink: %w", err)
		}
		fmt.Printf("==> Symlinked %s -> %s\n", targetPath, sourcePath)
	}

	return nil
}

// installStatus returns a human-readable string describing the current
// install state of the binary in ~/.local/bin.
func installStatus() string {
	exe, err := os.Executable()
	if err != nil {
		return "Install status: unknown (cannot determine executable path)"
	}
	currentPath, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "Install status: unknown (cannot resolve executable path)"
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "Install status: unknown (cannot determine home directory)"
	}
	installedPath := filepath.Join(home, ".local", "bin", filepath.Base(currentPath))

	info, err := os.Lstat(installedPath)
	if err != nil {
		return "Install status: not installed"
	}

	// Check if it's a symlink
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(installedPath)
		if err != nil {
			return fmt.Sprintf("Install status: %s (symlink, cannot read target)", installedPath)
		}
		if target == currentPath {
			return fmt.Sprintf("Install status: %s -> %s (current)", installedPath, target)
		}
		return fmt.Sprintf("Install status: %s -> %s (points elsewhere)", installedPath, target)
	}

	// Regular file — compare SHA-256
	currentHash, err := fileHash(currentPath)
	if err != nil {
		return fmt.Sprintf("Install status: %s (cannot hash current binary)", installedPath)
	}
	installedHash, err := fileHash(installedPath)
	if err != nil {
		return fmt.Sprintf("Install status: %s (cannot hash installed binary)", installedPath)
	}
	if currentHash == installedHash {
		return fmt.Sprintf("Install status: %s (copy, up to date)", installedPath)
	}
	return fmt.Sprintf("Install status: %s (copy, OUTDATED)", installedPath)
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
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
